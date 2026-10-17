/**
 * 任务表单（新建 / 编辑共用）——F05 §7.2 第 3 步。
 *
 * 契约要点（《A05》、F02 §6.4）：
 * - **新建**用 `CreateTaskBody`：**不接受** `status` / `progress`（新任务固定"未开始 / 0%"），
 *   因此这两个字段只在编辑模式出现；
 * - **编辑**用 `UpdateTaskBody` + `expected_updated_at`（由 `useVersionedForm` 注入），
 *   且**只提交用户实际改动的字段**（《A02》的教训：无变更的写会无谓推进版本）；
 * - `parent_id` 是**三态**：省略=不动、`null`=提升为顶层、给值=换父任务。
 *   本表单用"空串代表顶层任务"表达，提交时由 `buildTaskPatch` 翻译成 `null` 或省略
 *   （F02 §6.4 明确要求不要把三态交给 RHF 的"未挂载即 undefined"语义）；
 * - `description` 的**空串存 `""`**（与项目的"空串清空为 null"不同，见《A05》§端点 4）；
 * - 父任务候选来自 `POST /projects/{id}/tasks/query`（≤100 条）：只排除**自己**，
 *   层级深度与循环由后端校验（前端不重复实现，F02 §9）。
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
import FormControlLabel from '@mui/material/FormControlLabel';
import MenuItem from '@mui/material/MenuItem';
import Slider from '@mui/material/Slider';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { DatePicker } from '@mui/x-date-pickers/DatePicker';
import { queryProjectTasks } from '@/api/tasks';
import { toApiError, type ApiError } from '@/api/client';
import DateLocalizationProvider from '@/components/common/DateLocalizationProvider';
import { TASK_PRIORITY_OPTIONS, TASK_STATUS_OPTIONS } from '@/components/task/taskMeta';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import type { Task, TaskPriority, TaskStatus, UpdateTaskBody } from '@/types/task';
import { dayjsToNaturalDate, naturalDateToDayjs } from '@/utils/datetime';

/** 顶层任务的哨兵值：`''` 表示"没有父任务"（提交时翻译成 `null`） */
export const NO_PARENT = '';

export interface TaskFormValues {
  name: string;
  description: string;
  status: TaskStatus;
  progress: number;
  /** 0 低 / 1 普通 / 2 高 / 3 紧急；下拉框只提供这四个值，提交时按 `TaskPriority` 收窄 */
  priority: number;
  start_date: string;
  end_date: string;
  is_milestone: boolean;
  is_shared: boolean;
  parent_id: string;
}

const taskSchema = z.object({
  name: z.string().min(1, '请输入任务名称'),
  description: z.string(),
  status: z.enum(['not_started', 'in_progress', 'completed', 'cancelled']),
  progress: z.number().min(0, '进度不能小于 0').max(100, '进度不能大于 100'),
  // 用 z.number() 而不是字面量联合：表单值统一按 number 处理，避免 zodResolver 的推导类型
  // 与表单类型在 `priority` 上打架；收窄发生在 buildTaskPatch 里（下拉框只给 0–3）
  priority: z.number().int().min(0).max(3),
  start_date: z.string().min(1, '请选择开始日期'),
  end_date: z.string().min(1, '请选择结束日期'),
  is_milestone: z.boolean(),
  is_shared: z.boolean(),
  parent_id: z.string(),
});

/** 把任务对象翻成表单初值（空值统一成空串，便于"空串=顶层/清空"的语义） */
export function taskToFormValues(task: Task | null | undefined): TaskFormValues {
  return {
    name: task?.name ?? '',
    description: task?.description ?? '',
    status: task?.status ?? 'not_started',
    progress: task?.progress ?? 0,
    priority: task?.priority ?? 1,
    start_date: task?.start_date ?? '',
    end_date: task?.end_date ?? '',
    is_milestone: task?.is_milestone ?? false,
    is_shared: task?.is_shared ?? false,
    parent_id: task?.parent_id ?? NO_PARENT,
  };
}

export interface TaskFormDialogProps {
  open: boolean;
  mode: 'create' | 'edit';
  /** 任务所属项目（用于加载父任务候选） */
  projectId: string;
  /** 编辑模式的初始值 */
  task?: Task | null;
  submitting?: boolean;
  serverError?: ApiError | null;
  onClose: () => void;
  onSubmit: (values: TaskFormValues) => void;
}

export default function TaskFormDialog({
  open,
  mode,
  projectId,
  task,
  submitting = false,
  serverError = null,
  onClose,
  onSubmit,
}: TaskFormDialogProps) {
  const {
    control,
    register,
    handleSubmit,
    reset,
    setError,
    watch,
    formState: { errors },
  } = useForm<TaskFormValues>({
    resolver: zodResolver(taskSchema),
    defaultValues: taskToFormValues(task),
  });

  const [parentCandidates, setParentCandidates] = React.useState<Task[]>([]);
  const [parentLoading, setParentLoading] = React.useState(false);

  React.useEffect(() => {
    if (open) {
      reset(taskToFormValues(task));
    }
  }, [open, task, reset]);

  // 父任务候选：只取第一页（上限 100），排除自己
  React.useEffect(() => {
    if (!open || !projectId) {
      return undefined;
    }
    const controller = new AbortController();
    let cancelled = false;
    setParentLoading(true);
    void (async () => {
      try {
        const page = await queryProjectTasks(
          projectId,
          { page: 1, page_size: 100, sort: 'name' },
          controller.signal,
        );
        if (!cancelled) {
          setParentCandidates((page.data ?? []).filter((item) => item.id !== task?.id));
        }
      } catch {
        // 候选加载失败不阻塞表单：父任务留空即可（后端仍会校验）
        if (!cancelled) {
          setParentCandidates([]);
        }
      } finally {
        if (!cancelled) {
          setParentLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [open, projectId, task?.id]);

  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  const progressValue = watch('progress');
  const resolvedError = serverError ? toApiError(serverError) : null;
  const showGeneralError = resolvedError !== null && Object.keys(resolvedError.fields).length === 0;

  return (
    // 日期组件需要的 provider 跟着表单走（F01 §6 第 23 条）
    <DateLocalizationProvider>
      <Dialog open={open} onClose={submitting ? undefined : onClose} fullWidth maxWidth="sm">
        <Box
          component="form"
          onSubmit={handleSubmit((values) => onSubmit(values))}
          noValidate
          sx={{ display: 'flex', flexDirection: 'column' }}
        >
          <DialogTitle>{mode === 'create' ? '新建任务' : '编辑任务'}</DialogTitle>
          <DialogContent dividers>
            <Stack spacing={2.5} sx={{ pt: 1 }}>
              {showGeneralError ? (
                <Alert severity="error">{describeApiError(resolvedError)}</Alert>
              ) : null}

              <TextField
                {...register('name')}
                label="任务名称"
                autoFocus
                fullWidth
                error={Boolean(errors.name)}
                helperText={errors.name?.message}
              />

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

              {mode === 'edit' ? (
                <Stack direction="row" spacing={2} sx={{ alignItems: 'flex-start' }}>
                  <Controller
                    control={control}
                    name="status"
                    render={({ field }) => (
                      <TextField
                        select
                        label="状态"
                        sx={{ minWidth: 160 }}
                        value={field.value}
                        onChange={(event) => field.onChange(event.target.value)}
                        // 面向用户的短说明：不改技术细节，尽量保持一行
                        helperText="进度会同步更新状态"
                      >
                        {TASK_STATUS_OPTIONS.map((option) => (
                          <MenuItem key={option.value} value={option.value}>
                            {option.label}
                          </MenuItem>
                        ))}
                      </TextField>
                    )}
                  />
                  <Controller
                    control={control}
                    name="priority"
                    render={({ field }) => (
                      <TextField
                        select
                        label="优先级"
                        sx={{ minWidth: 140 }}
                        value={field.value}
                        onChange={(event) => field.onChange(Number(event.target.value))}
                        // 与状态框一样保留 helper 高度，两个下拉的输入框才能对齐
                        helperText={'\u00A0'}
                      >
                        {TASK_PRIORITY_OPTIONS.map((option) => (
                          <MenuItem key={option.value} value={option.value}>
                            {option.label}
                          </MenuItem>
                        ))}
                      </TextField>
                    )}
                  />
                </Stack>
              ) : (
                <>
                  <Controller
                    control={control}
                    name="priority"
                    render={({ field }) => (
                      <TextField
                        select
                        label="优先级"
                        fullWidth
                        value={field.value}
                        onChange={(event) => field.onChange(Number(event.target.value))}
                      >
                        {TASK_PRIORITY_OPTIONS.map((option) => (
                          <MenuItem key={option.value} value={option.value}>
                            {option.label}
                          </MenuItem>
                        ))}
                      </TextField>
                    )}
                  />
                  <Alert severity="info" variant="outlined">
                    新任务固定为"未开始 / 0%"，这两项在创建后到详情里调整。
                  </Alert>
                </>
              )}

              {mode === 'edit' ? (
                <Stack spacing={0.5} sx ={{ pt: 1, pb: 3, paddingLeft: 1, paddingRight: 1 }}>
                  <Stack direction="row" sx={{ justifyContent: 'space-between' }}>
                    <Typography variant="body2" color="text.secondary">
                      进度
                    </Typography>
                    <Typography variant="body2">{progressValue}%</Typography>
                  </Stack>
                  <Controller
                    control={control}
                    name="progress"
                    render={({ field }) => (
                      <Slider
                        value={field.value}
                        onChange={(_event, value) => field.onChange(value as number)}
                        step={5}
                        marks={[
                          { value: 0, label: '0' },
                          { value: 50, label: '50' },
                          { value: 100, label: '100' },
                        ]}
                        valueLabelDisplay="auto"
                      />
                    )}
                  />
                </Stack>
              ) : null}

              <Controller
                control={control}
                name="parent_id"
                render={({ field }) => (
                  <TextField
                    select
                    label="上级任务"
                    fullWidth
                    disabled={parentLoading}
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                    helperText="留空表示顶层任务；最多 3 层（候选只取前 100 个任务）"
                  >
                    <MenuItem value={NO_PARENT}>（顶层任务）</MenuItem>
                    {parentCandidates.map((candidate) => (
                      <MenuItem key={candidate.id} value={candidate.id}>
                        {candidate.name}
                      </MenuItem>
                    ))}
                  </TextField>
                )}
              />

              <Stack direction="row" spacing={3}>
                <FormControlLabel
                  control={
                    <Controller
                      control={control}
                      name="is_milestone"
                      render={({ field }) => (
                        <Switch
                          checked={field.value}
                          onChange={(event) => field.onChange(event.target.checked)}
                        />
                      )}
                    />
                  }
                  label="里程碑"
                />
                <FormControlLabel
                  control={
                    <Controller
                      control={control}
                      name="is_shared"
                      render={({ field }) => (
                        <Switch
                          checked={field.value}
                          onChange={(event) => field.onChange(event.target.checked)}
                        />
                      )}
                    />
                  }
                  label="共享给归属组"
                />
              </Stack>

              <TextField
                {...register('description')}
                label="描述"
                fullWidth
                multiline
                minRows={3}
                helperText="任务的空串存为空字符串（与项目「空串清空为 null」不同，《A05》§端点 4）"
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

/** 编辑模式：只把用户**实际改动**的字段放进 PATCH（含 `parent_id` 的三态翻译） */
export function buildTaskPatch(
  values: TaskFormValues,
  task: Task,
): Omit<UpdateTaskBody, 'expected_updated_at'> {
  const patch: Omit<UpdateTaskBody, 'expected_updated_at'> = {};
  if (values.name !== task.name) {
    patch.name = values.name;
  }
  if (values.description !== (task.description ?? '')) {
    patch.description = values.description;
  }
  if (values.status !== task.status) {
    patch.status = values.status;
  }
  if (values.progress !== task.progress) {
    patch.progress = values.progress;
  }
  if (values.priority !== task.priority) {
    // 下拉框只有 0–3，这里按契约类型收窄（越界值会被后端 422 拦住）
    patch.priority = values.priority as TaskPriority;
  }
  if (values.start_date !== task.start_date) {
    patch.start_date = values.start_date;
  }
  if (values.end_date !== task.end_date) {
    patch.end_date = values.end_date;
  }
  if (values.is_milestone !== task.is_milestone) {
    patch.is_milestone = values.is_milestone;
  }
  if (values.is_shared !== task.is_shared) {
    patch.is_shared = values.is_shared;
  }
  const currentParent = task.parent_id ?? NO_PARENT;
  if (values.parent_id !== currentParent) {
    // 三态：清空 → null（提升为顶层）；换父 → 具体 id；未改动 → 省略（这里自然省略）
    patch.parent_id = values.parent_id === NO_PARENT ? null : values.parent_id;
  }
  return patch;
}
