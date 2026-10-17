/**
 * 工作组新建 / 编辑表单（《A03》§1、§4）。
 *
 * - 新建只发送 `name` / `description`；`status` 由服务端固定为 active；
 * - 编辑可改 `name` / `description` / `status`；**所有者转移不在本表单**里
 *   （`owner_id` 有独立的确认动作，见 GroupMembersPanel）；
 * - `description` 清空按空串提交，服务端会存成 `null`。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import type { ApiError } from '@/api/client';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import { GROUP_STATUS_OPTIONS } from '@/components/group/groupMeta';
import type { Group, GroupStatus } from '@/types/group';

const schema = z.object({
  name: z.string().trim().min(1, '请输入工作组名称').max(160, '名称不超过 160 字符'),
  description: z.string().max(2000, '说明不超过 2000 字符'),
  status: z.enum(['active', 'disabled']),
});

export interface GroupFormValues {
  name: string;
  description: string;
  status: GroupStatus;
}

function toFormValues(group: Group | null | undefined): GroupFormValues {
  return {
    name: group?.name ?? '',
    description: group?.description ?? '',
    status: group?.status ?? 'active',
  };
}

export interface GroupFormDialogProps {
  open: boolean;
  mode: 'create' | 'edit';
  group?: Group | null;
  submitting?: boolean;
  serverError?: ApiError | null;
  onClose: () => void;
  onSubmit: (values: GroupFormValues) => void;
}

export default function GroupFormDialog({
  open,
  mode,
  group = null,
  submitting = false,
  serverError = null,
  onClose,
  onSubmit,
}: GroupFormDialogProps) {
  const {
    control,
    register,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = useForm<GroupFormValues>({
    resolver: zodResolver(schema),
    defaultValues: toFormValues(group),
  });

  React.useEffect(() => {
    if (open) {
      reset(toFormValues(group));
    }
  }, [group, open, reset]);

  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{mode === 'create' ? '新建工作组' : '编辑工作组'}</DialogTitle>
      <form onSubmit={handleSubmit((values) => onSubmit(values))}>
        <DialogContent>
          <Stack spacing={2}>
            {serverError ? <Alert severity="error">{describeApiError(serverError)}</Alert> : null}
            <TextField
              {...register('name')}
              label="工作组名称"
              autoFocus
              error={Boolean(errors.name)}
              helperText={errors.name?.message}
              fullWidth
            />
            <TextField
              {...register('description')}
              label="说明"
              multiline
              minRows={3}
              error={Boolean(errors.description)}
              helperText={errors.description?.message ?? '可留空；清空会存成 null'}
              fullWidth
            />
            {mode === 'edit' ? (
              <Controller
                control={control}
                name="status"
                render={({ field }) => (
                  <TextField
                    select
                    label="状态"
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                    helperText="停用后只有系统管理员可以重新启用"
                    fullWidth
                  >
                    {GROUP_STATUS_OPTIONS.map((option) => (
                      <MenuItem key={option.value} value={option.value}>
                        {option.label}
                      </MenuItem>
                    ))}
                  </TextField>
                )}
              />
            ) : null}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button type="submit" variant="contained" disabled={submitting}>
            {submitting ? '保存中…' : mode === 'create' ? '创建' : '保存'}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
