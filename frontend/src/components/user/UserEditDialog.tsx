/**
 * 编辑用户资料（《A02》§2）。
 *
 * 走 `useVersionedForm`：`expected_updated_at` 由 Hook 注入，`409 version_conflict`
 * 由 Hook 弹窗让用户选"放弃并刷新 / 保留修改"（F01 §5.3）。
 * `username` 不可修改；`status` 的停用/启用由列表页的专用动作处理，本表单只改
 * 资料字段、提醒开关与系统角色。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import FormControlLabel from '@mui/material/FormControlLabel';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { getUser, updateUser } from '@/api/users';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import { useNotifications } from '@/hooks/useNotifications';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import type { SystemRole, UpdateUserBody, User } from '@/types/user';
import { SYSTEM_ROLE_OPTIONS } from '@/components/user/userMeta';

const schema = z.object({
  display_name: z.string().trim().min(1, '请输入显示名').max(128, '显示名不超过 128 字符'),
  email: z
    .string()
    .min(3, '请输入邮箱')
    .max(320, '邮箱不超过 320 字符')
    .refine((value) => value.includes('@'), '邮箱格式看起来不正确'),
  timezone: z.string().min(1, '时区不能为空').max(64, '时区不超过 64 字符'),
  reminder_enabled: z.boolean(),
  system_role: z.enum(['member', 'admin']),
});

interface UserEditValues {
  display_name: string;
  email: string;
  timezone: string;
  reminder_enabled: boolean;
  system_role: SystemRole;
}

const EMPTY_VALUES: UserEditValues = {
  display_name: '',
  email: '',
  timezone: 'UTC',
  reminder_enabled: true,
  system_role: 'member',
};

function toFormValues(user: User): UserEditValues {
  return {
    display_name: user.display_name,
    email: user.email,
    timezone: user.timezone,
    reminder_enabled: user.reminder_enabled,
    system_role: user.system_role,
  };
}

function buildPatch(
  values: UserEditValues,
  user: User,
): Omit<UpdateUserBody, 'expected_updated_at'> {
  const patch: Omit<UpdateUserBody, 'expected_updated_at'> = {};
  if (values.display_name !== user.display_name) {
    patch.display_name = values.display_name;
  }
  if (values.email !== user.email) {
    patch.email = values.email;
  }
  if (values.timezone !== user.timezone) {
    patch.timezone = values.timezone;
  }
  if (values.reminder_enabled !== user.reminder_enabled) {
    patch.reminder_enabled = values.reminder_enabled;
  }
  if (values.system_role !== user.system_role) {
    patch.system_role = values.system_role;
  }
  return patch;
}

export interface UserEditDialogProps {
  /** 要编辑的用户；`null` 表示关闭 */
  userId: string | null;
  onClose: () => void;
  /** 保存成功或版本冲突刷新后通知列表重新加载 */
  onSaved: () => void;
}

export default function UserEditDialog({ userId, onClose, onSaved }: UserEditDialogProps) {
  const { show } = useNotifications();
  const [serverError, setServerError] = React.useState<unknown>(null);

  const {
    entity: user,
    loading,
    saving,
    error: loadError,
    save: saveUser,
  } = useVersionedForm<User, UpdateUserBody>({
    load: (signal) => getUser(userId as string, signal),
    save: (body) => updateUser(userId as string, body),
    autoLoad: userId !== null,
    conflictTitle: '用户资料已被其他人修改',
  });

  const {
    register,
    control,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = useForm<UserEditValues>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY_VALUES,
  });

  React.useEffect(() => {
    if (user) {
      reset(toFormValues(user));
    }
  }, [reset, user]);

  React.useEffect(() => {
    setServerError(null);
    if (userId === null) {
      reset(EMPTY_VALUES);
    }
  }, [reset, userId]);

  React.useEffect(() => {
    if (serverError) {
      applyApiFieldErrors(serverError, setError);
    }
  }, [serverError, setError]);

  const onSubmit = handleSubmit(async (values) => {
    if (!user) {
      return;
    }
    const patch = buildPatch(values, user);
    if (Object.keys(patch).length === 0) {
      show('没有需要保存的修改。', { severity: 'info' });
      onClose();
      return;
    }
    setServerError(null);
    const outcome = await saveUser(patch);
    if (outcome.ok) {
      show('用户资料已保存。', { severity: 'success' });
      onSaved();
      onClose();
      return;
    }
    if (outcome.resolution === 'reloaded') {
      show('已刷新为服务端最新数据。', { severity: 'info' });
      onSaved();
      onClose();
      return;
    }
    setServerError(outcome.error);
    if (outcome.resolution === 'kept') {
      show('版本号已刷新为服务端最新值，可以再次提交。', { severity: 'warning' });
    }
  });

  return (
    <Dialog open={userId !== null} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>编辑用户</DialogTitle>
      {loading && !user ? (
        <DialogContent>
          <Stack sx={{ alignItems: 'center', py: 4 }}>
            <CircularProgress size={28} />
          </Stack>
        </DialogContent>
      ) : (
        <form onSubmit={onSubmit}>
          <DialogContent>
            <Stack spacing={2}>
              {loadError ? <Alert severity="error">{describeApiError(loadError)}</Alert> : null}
              {serverError ? (
                <Alert severity="error">{describeApiError(serverError)}</Alert>
              ) : null}
              <TextField
                label="用户名"
                value={user?.username ?? ''}
                disabled
                helperText="登录标识，创建后不可修改"
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
                {...register('timezone')}
                label="时区"
                error={Boolean(errors.timezone)}
                helperText={errors.timezone?.message ?? 'IANA 时区，例如 Asia/Shanghai'}
                fullWidth
              />
              <Controller
                control={control}
                name="reminder_enabled"
                render={({ field }) => (
                  <FormControlLabel
                    control={
                      <Switch
                        checked={field.value}
                        onChange={(event) => field.onChange(event.target.checked)}
                      />
                    }
                    label="启用邮件提醒"
                  />
                )}
              />
              <Controller
                control={control}
                name="system_role"
                render={({ field }) => (
                  <TextField
                    select
                    label="系统角色"
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                    helperText="系统至少要保留一个启用中的管理员；降级最后一个管理员会返回 409"
                    fullWidth
                  >
                    {SYSTEM_ROLE_OPTIONS.map((option) => (
                      <MenuItem key={option.value} value={option.value}>
                        {option.label}
                      </MenuItem>
                    ))}
                  </TextField>
                )}
              />
            </Stack>
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose} disabled={saving}>
              取消
            </Button>
            <Button type="submit" variant="contained" disabled={saving || !user}>
              {saving ? '保存中…' : '保存'}
            </Button>
          </DialogActions>
        </form>
      )}
    </Dialog>
  );
}
