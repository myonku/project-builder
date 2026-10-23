/**
 * 个人信息页（`/me`，交互与头像规则见 F01 §5.12）。
 *
 * 资料读取用 `GET /auth/me`/`GET /users/{id}`，保存用
 * `PATCH /users/{id}` + `useVersionedForm`。本人只允许改 display_name / email /
 * timezone / reminder_enabled；username/status/system_role 只读。
 *
 * 头像经前端方形裁剪后上传；`POST/DELETE /me/avatar` 成功后立即刷新顶栏与页面。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import FormControlLabel from '@mui/material/FormControlLabel';
import Grid from '@mui/material/Grid';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutlined';
import SaveIcon from '@mui/icons-material/Save';
import UploadIcon from '@mui/icons-material/Upload';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { deleteAvatar, getUser, updateUser, uploadAvatar } from '@/api/users';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import UserAvatar from '@/components/common/UserAvatar';
import { systemRoleMeta, userStatusMeta } from '@/components/user/userMeta';
import { applyApiFieldErrors, describeApiError } from '@/hooks/useApiError';
import { useNotifications } from '@/hooks/useNotifications';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import { useAuthStore } from '@/stores/auth';
import type { UpdateUserBody, User } from '@/types/user';
import { formatForDisplay } from '@/utils/datetime';

const AvatarCropDialog = React.lazy(() => import('@/components/user/AvatarCropDialog'));

const schema = z.object({
  display_name: z.string().trim().min(1, '请输入显示名').max(128, '显示名不超过 128 字符'),
  email: z
    .string()
    .min(3, '请输入邮箱')
    .max(320, '邮箱不超过 320 字符')
    .refine((value) => value.includes('@'), '邮箱格式看起来不正确'),
  timezone: z.string().min(1, '时区不能为空').max(64, '时区不超过 64 字符'),
  reminder_enabled: z.boolean(),
});

type ProfileValues = z.infer<typeof schema>;

const EMPTY_VALUES: ProfileValues = {
  display_name: '',
  email: '',
  timezone: 'UTC',
  reminder_enabled: true,
};

function toFormValues(user: User): ProfileValues {
  return {
    display_name: user.display_name,
    email: user.email,
    timezone: user.timezone,
    reminder_enabled: user.reminder_enabled,
  };
}

function buildPatch(values: ProfileValues, user: User): Omit<UpdateUserBody, 'expected_updated_at'> {
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
  return patch;
}

export default function ProfileView() {
  const currentUser = useAuthStore((state) => state.user);
  const setUser = useAuthStore((state) => state.setUser);
  const { show } = useNotifications();
  const [serverError, setServerError] = React.useState<unknown>(null);
  const [avatarVersion, setAvatarVersion] = React.useState<string | null | undefined>(undefined);
  const [avatarFile, setAvatarFile] = React.useState<File | null>(null);
  const [avatarBusy, setAvatarBusy] = React.useState(false);
  const [avatarError, setAvatarError] = React.useState<unknown>(null);
  const fileInputRef = React.useRef<HTMLInputElement | null>(null);

  const {
    entity: user,
    loading,
    saving,
    error: loadError,
    save,
  } = useVersionedForm<User, UpdateUserBody>({
    load: (signal) => getUser(currentUser?.id ?? '', signal),
    save: (body) => updateUser(currentUser?.id ?? '', body),
    autoLoad: Boolean(currentUser?.id),
    conflictTitle: '个人信息已被其他人修改',
  });

  const displayedAvatarVersion = avatarVersion === undefined ? user?.avatar_updated_at : avatarVersion;
  const displayedUser = user ? { ...user, avatar_updated_at: displayedAvatarVersion ?? null } : null;

  const {
    control,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = useForm<ProfileValues>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY_VALUES,
  });

  React.useEffect(() => {
    if (user) {
      reset(toFormValues(user));
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
    const patch = buildPatch(values, user);
    if (Object.keys(patch).length === 0) {
      show('没有需要保存的修改。', { severity: 'info' });
      return;
    }
    setServerError(null);
    const outcome = await save(patch);
    if (outcome.ok) {
      setUser(outcome.entity);
      setAvatarVersion(undefined);
      show('个人信息已保存。', { severity: 'success' });
      return;
    }
    if (outcome.resolution === 'reloaded') {
      show('已加载最新信息。', { severity: 'info' });
      return;
    }
    setServerError(outcome.error);
    if (outcome.resolution === 'kept') {
      show('已保留你的修改，请检查后再次保存。', { severity: 'warning' });
    }
  });

  const handleAvatarFile = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) {
      return;
    }
    setAvatarError(null);
    if (file.size > 10 * 1024 * 1024) {
      setAvatarError(new Error('原图不得超过 10 MiB，请选择较小的图片。'));
      return;
    }
    let header: Uint8Array;
    try { header = new Uint8Array(await file.slice(0, 8).arrayBuffer()); }
    catch { setAvatarError(new Error('图片无法读取，请重新选择。')); return; }
    const png = [137, 80, 78, 71, 13, 10, 26, 10].every((value, i) => header[i] === value);
    const jpeg = header[0] === 255 && header[1] === 216 && header[2] === 255;
    if (!png && !jpeg) {
      setAvatarError(new Error('请选择 PNG 或 JPEG 图片。'));
      return;
    }
    setAvatarFile(file);
  };

  const handleSaveAvatar = async (file: File) => {
    setAvatarBusy(true);
    setAvatarError(null);
    try {
      const saved = await uploadAvatar(file);
      setUser(saved);
      setAvatarVersion(saved.avatar_updated_at);
      setAvatarFile(null);
      show('头像已更新。', { severity: 'success' });
    } catch (error) {
      setAvatarError(error);
    } finally {
      setAvatarBusy(false);
    }
  };

  const handleRemoveAvatar = async () => {
    if (!user) {
      return;
    }
    setAvatarBusy(true);
    setAvatarError(null);
    try {
      await deleteAvatar();
      setAvatarVersion(null);
      setUser({ ...user, avatar_updated_at: null });
      show('头像已移除。', { severity: 'success' });
    } catch (error) {
      setAvatarError(error);
    } finally {
      setAvatarBusy(false);
    }
  };

  const statusMeta = user ? userStatusMeta(user.status) : null;
  const roleMeta = user ? systemRoleMeta(user.system_role) : null;

  return (
    <PageContainer
      title="个人信息"
      breadcrumbs={[{ title: '首页', path: '/' }, { title: '个人信息' }]}
    >
      {loading && !user ? (
        <Stack sx={{ alignItems: 'center', py: 8 }}>
          <CircularProgress size={32} />
        </Stack>
      ) : (
        <Grid container spacing={3}>
          <Grid size={{ xs: 12, md: 4 }}>
            <Card variant="outlined">
              <CardContent>
                {loadError ? (
                  <Alert severity="error" sx={{ mb: 2 }}>
                    {describeApiError(loadError)}
                  </Alert>
                ) : null}
                <Stack spacing={2} sx={{ alignItems: 'center', textAlign: 'center' }}>
                  <UserAvatar user={displayedUser} size={144} />
                  <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', justifyContent: 'center' }}>
                    <Button
                      size="small"
                      variant="outlined"
                      startIcon={avatarBusy ? <CircularProgress size={14} /> : <UploadIcon />}
                      disabled={avatarBusy || !user}
                      onClick={() => fileInputRef.current?.click()}
                    >
                      上传头像
                    </Button>
                    {displayedAvatarVersion ? (
                      <Button
                        size="small"
                        color="inherit"
                        startIcon={<DeleteOutlineIcon />}
                        disabled={avatarBusy}
                        onClick={handleRemoveAvatar}
                      >
                        移除
                      </Button>
                    ) : null}
                  </Stack>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept="image/png,image/jpeg"
                    hidden
                    onChange={handleAvatarFile}
                  />
                  <Typography variant="caption" color="text.secondary">支持 PNG/JPEG，原图不超过 10 MiB；裁剪后保存。</Typography>
                  {avatarError && !avatarFile ? <Alert severity="error">{describeApiError(avatarError)}</Alert> : null}
                  <Box>
                    <Typography variant="h6">{user?.display_name ?? '—'}</Typography>
                    <Typography variant="body2" color="text.secondary">
                      @{user?.username ?? '—'}
                    </Typography>
                  </Box>
                  <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', justifyContent: 'center' }}>
                    {statusMeta ? <StatusTag label={statusMeta.label} tone={statusMeta.tone} /> : null}
                    {roleMeta ? <StatusTag label={roleMeta.label} tone={roleMeta.tone} /> : null}
                  </Stack>
                </Stack>
                <Divider sx={{ my: 2 }} />
                <Stack spacing={1.25}>
                  <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
                    <Typography variant="body2" color="text.secondary">
                      用户名
                    </Typography>
                    <Typography variant="body2">{user?.username ?? '—'}</Typography>
                  </Stack>
                  <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
                    <Typography variant="body2" color="text.secondary">
                      邮箱
                    </Typography>
                    <Typography variant="body2" sx={{ wordBreak: 'break-all', textAlign: 'right' }}>
                      {user?.email ?? '—'}
                    </Typography>
                  </Stack>
                  <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
                    <Typography variant="body2" color="text.secondary">
                      时区
                    </Typography>
                    <Typography variant="body2">{user?.timezone ?? '—'}</Typography>
                  </Stack>
                  <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
                    <Typography variant="body2" color="text.secondary">
                      上次登录
                    </Typography>
                    <Typography variant="body2">
                      {formatForDisplay(user?.last_login_at, user?.timezone)}
                    </Typography>
                  </Stack>
                </Stack>
              </CardContent>
            </Card>
          </Grid>

          <Grid size={{ xs: 12, md: 8 }}>
            <Card variant="outlined">
              <CardContent>
                <Stack spacing={2}>
                  <Box>
                    <Typography variant="h6" component="h2">
                      编辑资料
                    </Typography>
                    <Typography variant="body2" color="text.secondary">
                      可修改显示名、邮箱、时区和提醒偏好；账号权限由管理员维护。
                    </Typography>
                  </Box>

                  {serverError ? (
                    <Alert severity="error">{describeApiError(serverError)}</Alert>
                  ) : null}

                  <Box component="form" onSubmit={onSubmit} noValidate>
                    <Stack spacing={2}>
                      <Controller
                        control={control}
                        name="display_name"
                        render={({ field }) => (
                          <TextField
                            label="显示名"
                            value={field.value}
                            onChange={field.onChange}
                            onBlur={field.onBlur}
                            name={field.name}
                            inputRef={field.ref}
                            error={Boolean(errors.display_name)}
                            helperText={errors.display_name?.message ?? '用于向其他成员展示'}
                            fullWidth
                          />
                        )}
                      />
                      <Controller
                        control={control}
                        name="email"
                        render={({ field }) => (
                          <TextField
                            label="邮箱"
                            type="email"
                            value={field.value}
                            onChange={field.onChange}
                            onBlur={field.onBlur}
                            name={field.name}
                            inputRef={field.ref}
                            error={Boolean(errors.email)}
                            helperText={errors.email?.message ?? '用于提醒邮件与密码找回'}
                            fullWidth
                          />
                        )}
                      />
                      <Controller
                        control={control}
                        name="timezone"
                        render={({ field }) => (
                          <TextField
                            label="时区"
                            value={field.value}
                            onChange={field.onChange}
                            onBlur={field.onBlur}
                            name={field.name}
                            inputRef={field.ref}
                            error={Boolean(errors.timezone)}
                            helperText={errors.timezone?.message ?? '例如 Asia/Shanghai'}
                            fullWidth
                          />
                        )}
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
                            label="接收邮件提醒"
                          />
                        )}
                      />
                      <Stack direction="row" spacing={1} sx={{ justifyContent: 'flex-end' }}>
 <Button href="/forgot-password">通过邮箱重置密码</Button>
                        <Button
                          type="button"
                          onClick={() => {
                            if (user) {
                              reset(toFormValues(user));
                              setServerError(null);
                            }
                          }}
                          disabled={saving}
                        >
                          重置
                        </Button>
                        <Button
                          type="submit"
                          variant="contained"
                          startIcon={<SaveIcon />}
                          disabled={saving || !user}
                        >
                          {saving ? '保存中…' : '保存'}
                        </Button>
                      </Stack>
                    </Stack>
                  </Box>
                </Stack>
              </CardContent>
            </Card>
          </Grid>
        </Grid>
      )}
      {avatarFile && <React.Suspense fallback={<CircularProgress size={24} aria-label="正在准备裁剪" />}>
        <AvatarCropDialog file={avatarFile} busy={avatarBusy} error={avatarError}
          onCancel={() => { setAvatarFile(null); setAvatarError(null); }} onSave={handleSaveAvatar} />
      </React.Suspense>}
    </PageContainer>
  );
}
