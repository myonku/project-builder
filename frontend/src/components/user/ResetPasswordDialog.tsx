/**
 * 管理员重置用户密码（《A02》§6）。
 *
 * 字段名是 `new_password`（不是 `password`）；必须带目标当前的 `updated_at`。
 * 重置后旧密码与既有会话失效。
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
import { resetPassword } from '@/api/users';
import { toApiError, type ApiError } from '@/api/client';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import type { User } from '@/types/user';

const schema = z
  .object({
    new_password: z.string().min(8, '密码至少 8 位').max(256, '密码不超过 256 字符'),
    confirm_password: z.string(),
  })
  .refine((values) => values.new_password === values.confirm_password, {
    path: ['confirm_password'],
    message: '两次输入的密码不一致',
  });

interface ResetPasswordValues {
  new_password: string;
  confirm_password: string;
}

const EMPTY_VALUES: ResetPasswordValues = { new_password: '', confirm_password: '' };

export interface ResetPasswordDialogProps {
  /** 目标用户；`null` 表示关闭 */
  user: User | null;
  onClose: () => void;
  /** 重置成功：刷新列表并关闭 */
  onDone: () => void;
  /** 版本冲突后刷新列表 */
  onReload: () => void;
}

export default function ResetPasswordDialog({
  user,
  onClose,
  onDone,
  onReload,
}: ResetPasswordDialogProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const [serverError, setServerError] = React.useState<ApiError | null>(null);
  const [submitting, setSubmitting] = React.useState(false);

  const {
    register,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = useForm<ResetPasswordValues>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY_VALUES,
  });

  React.useEffect(() => {
    setServerError(null);
    if (user) {
      reset(EMPTY_VALUES);
    }
  }, [reset, user]);

  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  const onSubmit = handleSubmit(async (values) => {
    if (!user) {
      return;
    }
    setSubmitting(true);
    setServerError(null);
    try {
      await resetPassword(user.id, {
        new_password: values.new_password,
        expected_updated_at: user.updated_at,
      });
      show(`已重置「${user.display_name}」的密码。`, { severity: 'success' });
      onDone();
    } catch (caught) {
      const apiError = toApiError(caught);
      setServerError(apiError);
      if (apiError.isVersionConflict) {
        const refresh = await dialogs.confirm(
          '该用户资料在你操作期间已被其他人修改。请刷新列表后重新打开重置密码。',
          {
            title: '用户资料已变化',
            okText: '刷新并关闭',
            cancelText: '关闭',
            severity: 'warning',
          },
        );
        if (refresh) {
          onReload();
        }
        onClose();
      }
    } finally {
      setSubmitting(false);
    }
  });

  return (
    <Dialog open={user !== null} onClose={onClose} fullWidth maxWidth="xs">
      <DialogTitle>重置密码</DialogTitle>
      <form onSubmit={onSubmit}>
        <DialogContent>
          <Stack spacing={2}>
            <Alert severity="info" variant="outlined">
              目标用户：{user?.display_name}（{user?.username}）。重置后旧密码立即失效；
              该用户已有会话将失效，需要重新登录。
            </Alert>
            {serverError ? (
              <Alert severity="error">{describeApiError(serverError)}</Alert>
            ) : null}
            <TextField
              {...register('new_password')}
              label="新密码"
              type="password"
              autoComplete="new-password"
              autoFocus
              error={Boolean(errors.new_password)}
              helperText={errors.new_password?.message ?? '至少 8 位'}
              fullWidth
            />
            <TextField
              {...register('confirm_password')}
              label="确认新密码"
              type="password"
              autoComplete="new-password"
              error={Boolean(errors.confirm_password)}
              helperText={errors.confirm_password?.message}
              fullWidth
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button type="submit" variant="contained" disabled={submitting || !user}>
            {submitting ? '重置中…' : '重置密码'}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
