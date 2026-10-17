/**
 * 新建用户对话框（《A02》§4）。
 *
 * - 创建端点**不接受** `system_role`：新账号固定 `member`，角色变更走 `PATCH /users/{id}`；
 * - 状态、提醒开关同样由服务端固定为 `active` / `true`，不放在本表单；
 * - `timezone` 省略/空串由服务端存为 `UTC`。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import type { ApiError } from '@/api/client';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';

const schema = z.object({
  username: z
    .string()
    .regex(/^[A-Za-z0-9_-]{3,64}$/, '用户名 3–64 位，仅限字母、数字、_ 和 -'),
  email: z
    .string()
    .min(3, '请输入邮箱')
    .max(320, '邮箱不超过 320 字符')
    .refine((value) => value.includes('@'), '邮箱格式看起来不正确'),
  display_name: z.string().trim().min(1, '请输入显示名').max(128, '显示名不超过 128 字符'),
  password: z.string().min(8, '密码至少 8 位').max(256, '密码不超过 256 字符'),
  timezone: z.string().max(64, '时区不超过 64 字符'),
});

export interface UserCreateValues {
  username: string;
  email: string;
  display_name: string;
  password: string;
  timezone: string;
}

const EMPTY_VALUES: UserCreateValues = {
  username: '',
  email: '',
  display_name: '',
  password: '',
  timezone: '',
};

export interface UserCreateDialogProps {
  open: boolean;
  submitting?: boolean;
  serverError?: ApiError | null;
  onClose: () => void;
  onSubmit: (values: UserCreateValues) => void;
}

export default function UserCreateDialog({
  open,
  submitting = false,
  serverError = null,
  onClose,
  onSubmit,
}: UserCreateDialogProps) {
  const {
    register,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = useForm<UserCreateValues>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY_VALUES,
  });

  React.useEffect(() => {
    if (open) {
      reset(EMPTY_VALUES);
    }
  }, [open, reset]);

  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>新建用户</DialogTitle>
      <form onSubmit={handleSubmit((values) => onSubmit(values))}>
        <DialogContent>
          <Stack spacing={2}>
            {serverError ? <Alert severity="error">{describeApiError(serverError)}</Alert> : null}
            <TextField
              {...register('username')}
              label="用户名"
              helperText={errors.username?.message ?? '登录标识，创建后不可修改'}
              error={Boolean(errors.username)}
              autoFocus
              fullWidth
            />
            <TextField
              {...register('display_name')}
              label="显示名"
              error={Boolean(errors.display_name)}
              helperText={errors.display_name?.message}
              fullWidth
            />
            <TextField
              {...register('email')}
              label="邮箱"
              type="email"
              error={Boolean(errors.email)}
              helperText={errors.email?.message}
              fullWidth
            />
            <TextField
              {...register('password')}
              label="初始密码"
              type="password"
              autoComplete="new-password"
              error={Boolean(errors.password)}
              helperText={errors.password?.message ?? '至少 8 位；用户首次登录后请自行修改'}
              fullWidth
            />
            <TextField
              {...register('timezone')}
              label="时区"
              placeholder="Asia/Shanghai"
              error={Boolean(errors.timezone)}
              helperText={errors.timezone?.message ?? 'IANA 时区，留空为 UTC'}
              fullWidth
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button type="submit" variant="contained" disabled={submitting}>
            {submitting ? '创建中…' : '创建'}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
