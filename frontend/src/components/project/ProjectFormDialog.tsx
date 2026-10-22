/**
 * 项目表单（新建 / 编辑共用）。
 *
 * 用**对话框**而不是路由页：新建与编辑的字段集合只差"归属工作组"，且都从列表/详情页发起，
 * 关掉就回到原页面，筛选与滚动位置天然保留（与《F01》§5.7 对任务详情选 Drawer 的理由同源）。
 *
 * 契约要点（《A04》、F02 §6.3）：
 * - **归属工作组只在新建时可填**：`UpdateProjectBody` 里没有 `group_id`，编辑时显示为只读；
 * - `description` / `color` 的**空串表示清空**（后端存 `null`）；
 * - 日期是 `YYYY-MM-DD` 自然日：`DatePicker` 只在字段边界做 `Dayjs` 转换，
 *   表单状态里始终是字符串（F02 §3）；日期区间的**语义校验交给后端**，
 *   前端只提示不拦截（F02 §9）；
 * - `expected_updated_at` 不出现在表单里，由调用方（`useVersionedForm`）注入。
 *
 * 字段级错误：`422` 的 `fields` 由本组件用 `applyApiFieldErrors` 回填到 RHF，
 * 其余错误显示为对话框顶部的 Alert（不关掉对话框，用户改完再提交）。
 */

import * as React from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import InputAdornment from '@mui/material/InputAdornment';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { DatePicker } from '@mui/x-date-pickers/DatePicker';
import DateLocalizationProvider from '@/components/common/DateLocalizationProvider';
import { toApiError, type ApiError } from '@/api/client';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import type { GroupOption } from '@/hooks/useGroupOptions';
import type { Project, ProjectVisibility } from '@/types/project';
import { dayjsToNaturalDate, naturalDateToDayjs } from '@/utils/datetime';

/** 表单值：字段集合与《F02》§6.3 的 `CreateProjectBody` / `UpdateProjectBody` 对齐（去掉版本字段） */
export interface ProjectFormValues {
  name: string;
  group_id: string;
  start_date: string;
  end_date: string;
  description: string;
  visibility: ProjectVisibility;
  color: string;
}

/** 只做结构校验：必填与类型；长度、日期区间、共享级别的业务含义由后端判定（F02 §9） */
const projectSchema = z.object({
  name: z.string().min(1, '请输入项目名称'),
  group_id: z.string().min(1, '请选择归属工作组'),
  start_date: z.string().min(1, '请选择开始日期'),
  end_date: z.string().min(1, '请选择结束日期'),
  description: z.string(),
  visibility: z.enum(['private', 'group_readonly', 'group_editable']),
  color: z.string(),
});

const VISIBILITY_OPTIONS: { value: ProjectVisibility; label: string; hint: string }[] = [
  { value: 'private', label: '私有', hint: '仅项目成员可见' },
  { value: 'group_readonly', label: '组内只读', hint: '归属组成员可见（含已共享任务），不能改' },
  { value: 'group_editable', label: '组内可编辑', hint: '归属组成员可改已共享任务，不能改项目本身' },
];

export interface ProjectFormDialogProps {
  open: boolean;
  mode: 'create' | 'edit';
  /** 编辑模式的初始值；新建模式传 `undefined` */
  project?: Project | null;
  groups: GroupOption[];
  /** 提交中：禁用按钮并显示 loading */
  submitting?: boolean;
  /** 上次提交的错误：用于 Alert 与字段回填 */
  serverError?: ApiError | null;
  onClose: () => void;
  onSubmit: (values: ProjectFormValues) => void;
}

/** 把 `Project` 转成表单初值（空值统一成空串，便于"空串=清空"的语义） */
export function projectToFormValues(project: Project | null | undefined): ProjectFormValues {
  return {
    name: project?.name ?? '',
    group_id: project?.group_id ?? '',
    start_date: project?.start_date ?? '',
    end_date: project?.end_date ?? '',
    description: project?.description ?? '',
    visibility: project?.visibility ?? 'private',
    color: project?.color ?? '',
  };
}

export default function ProjectFormDialog({
  open,
  mode,
  project,
  groups,
  submitting = false,
  serverError = null,
  onClose,
  onSubmit,
}: ProjectFormDialogProps) {
  const {
    control,
    register,
    handleSubmit,
    reset,
    setError,
    watch,
    formState: { errors },
  } = useForm<ProjectFormValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: projectToFormValues(project),
  });

  // 打开时用最新的对象重置表单（编辑模式下"放弃修改"后重新打开要拿到服务端最新值）
  React.useEffect(() => {
    if (open) {
      reset(projectToFormValues(project));
    }
  }, [open, project, reset]);

  // 422 的 fields 回填到对应字段；其它错误在下面的 Alert 里显示
  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  // 用 watch 读取当前值做预览与提示；不要碰 react-hook-form 的内部字段（_formValues 不是公开 API）
  const colorValue = watch('color');
  const visibilityValue = watch('visibility');

  const resolvedError = serverError ? toApiError(serverError) : null;
  // 有字段级明细时不再重复整段 Alert；没有明细时才提示整体原因
  const showGeneralError =
    resolvedError !== null && Object.keys(resolvedError.fields).length === 0;

  return (
    // 日期组件需要的 provider 跟着本对话框走，不挂 App 顶层（F04 D25）
    <DateLocalizationProvider>
      <Dialog open={open} onClose={submitting ? undefined : onClose} fullWidth maxWidth="sm">
      <Box
        component="form"
        onSubmit={handleSubmit((values) => onSubmit(values))}
        noValidate
        sx={{ display: 'flex', flexDirection: 'column' }}
      >
        <DialogTitle>{mode === 'create' ? '新建项目' : '编辑项目'}</DialogTitle>
        <DialogContent dividers>
          <Stack spacing={2.5} sx={{ pt: 1 }}>
            {showGeneralError ? (
              <Alert severity="error">{describeApiError(resolvedError)}</Alert>
            ) : null}

            <TextField
              {...register('name')}
              label="项目名称"
              autoFocus
              fullWidth
              error={Boolean(errors.name)}
              helperText={errors.name?.message}
            />

            {mode === 'create' ? (
              <Controller
                control={control}
                name="group_id"
                render={({ field }) => (
                  <TextField
                    select
                    label="归属工作组"
                    fullWidth
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                    error={Boolean(errors.group_id)}
                    helperText={
                      errors.group_id?.message ??
                      '请选择你已加入且未被移除的工作组'
                    }
                  >
                    {groups.map((group) => (
                      <MenuItem key={group.id} value={group.id}>
                        {group.name}
                      </MenuItem>
                    ))}
                  </TextField>
                )}
              />
            ) : (
              <TextField
                label="归属工作组"
                value={groups.find((group) => group.id === project?.group_id)?.name ?? '—'}
                fullWidth
                disabled
                helperText="创建后不能更换工作组"
              />
            )}

            <Stack direction="row" spacing={2}>
              <Controller
                control={control}
                name="start_date"
                render={({ field }) => (
                  <DatePicker
                    label="开始日期"
                    value={naturalDateToDayjs(field.value)}
                    onChange={(value) => field.onChange(dayjsToNaturalDate(value) ?? '')}
                    format="YYYY-MM-DD"
                    slotProps={{
                      textField: {
                        fullWidth: true,
                        error: Boolean(errors.start_date),
                        helperText: errors.start_date?.message,
                      },
                    }}
                  />
                )}
              />
              <Controller
                control={control}
                name="end_date"
                render={({ field }) => (
                  <DatePicker
                    label="结束日期"
                    value={naturalDateToDayjs(field.value)}
                    onChange={(value) => field.onChange(dayjsToNaturalDate(value) ?? '')}
                    format="YYYY-MM-DD"
                    slotProps={{
                      textField: {
                        fullWidth: true,
                        error: Boolean(errors.end_date),
                        helperText: errors.end_date?.message,
                      },
                    }}
                  />
                )}
              />
            </Stack>

            <Controller
              control={control}
              name="visibility"
              render={({ field }) => (
                <TextField
                  select
                  label="共享级别"
                  fullWidth
                  value={field.value}
                  onChange={(event) => field.onChange(event.target.value)}
                  helperText={
                    VISIBILITY_OPTIONS.find((option) => option.value === visibilityValue)?.hint
                  }
                >
                  {VISIBILITY_OPTIONS.map((option) => (
                    <MenuItem key={option.value} value={option.value}>
                      {option.label}
                    </MenuItem>
                  ))}
                </TextField>
              )}
            />

            <TextField
              {...register('color')}
              label="颜色"
              placeholder="#336699"
              fullWidth
              slotProps={{
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <Box
                        sx={{
                          width: 18,
                          height: 18,
                          borderRadius: 0.5,
                          border: 1,
                          borderColor: 'divider',
                          bgcolor: colorValue || 'transparent',
                        }}
                      />
                    </InputAdornment>
                  ),
                },
              }}
              helperText="最多 32 个字符，可留空"
            />

            <TextField
              {...register('description')}
              label="描述"
              fullWidth
              multiline
              minRows={3}
              helperText="填写补充说明，可留空"
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button type="submit" variant="contained" loading={submitting}>
            {mode === 'create' ? '创建' : '保存'}
          </Button>
        </DialogActions>
      </Box>
      </Dialog>
    </DateLocalizationProvider>
  );
}
