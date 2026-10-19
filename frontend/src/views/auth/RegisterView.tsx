/**
 * 自助注册页（提交申请，不自动登录）。
 *
 * 生产配置下 POST /auth/register 只创建待审核申请；管理员在
 * /admin/registration-requests 通过后才创建账号。
 *
 * 表单只做结构校验（必填、邮箱格式、两次密码一致），用户名/邮箱长度、密码强度等
 * 语义规则仍由后端判定（F02 §9）；后端 422/409 的 fields 会回填到字段。
 */

import * as React from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Link from '@mui/material/Link';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useForm } from 'react-hook-form';
import { useNavigate } from 'react-router';
import { z } from 'zod';
import { register } from '@/api/auth';
import { describeApiError, applyApiFieldErrors } from '@/hooks/useApiError';
import { useNotifications } from '@/hooks/useNotifications';
import type { RegistrationRequest } from '@/types/registration';

const schema = z
  .object({
    username: z.string().min(1, '请输入用户名'),
    email: z.string().min(1, '请输入邮箱').email('邮箱格式不正确'),
    display_name: z.string().min(1, '请输入显示名称'),
    password: z.string().min(1, '请输入密码'),
    confirm_password: z.string(),
    timezone: z.string().optional(),
  })
  .refine((values) => values.password === values.confirm_password, {
    path: ['confirm_password'],
    message: '两次输入的密码不一致',
  });

type RegisterFormValues = z.infer<typeof schema>;

export default function RegisterView() {
  const navigate = useNavigate();
  const { show } = useNotifications();
  const [submitted, setSubmitted] = React.useState<RegistrationRequest | null>(null);
  const [serverError, setServerError] = React.useState<string | null>(null);

  const {
    register: field,
    handleSubmit,
    reset,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<RegisterFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      username: '',
      email: '',
      display_name: '',
      password: '',
      confirm_password: '',
      timezone: '',
    },
  });

  const onSubmit = handleSubmit(async (values) => {
    setServerError(null);
    try {
      const result = await register({
        username: values.username.trim(),
        email: values.email.trim(),
        display_name: values.display_name.trim(),
        password: values.password,
        timezone: values.timezone?.trim() || undefined,
      });
      // 测试配置可能关闭审核并直接返回 User；生产配置返回申请。
      if ('system_role' in result) {
        reset();
        show('账号已创建，请使用新账号登录。', { severity: 'success' });
        navigate('/login');
        return;
      }
      setSubmitted(result);
      reset();
    } catch (caught) {
      if (applyApiFieldErrors(caught, setError)) {
        return;
      }
      setServerError(describeApiError(caught));
    }
  });

  return (
    <Box
      sx={{
        flex: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        py: 4,
      }}
    >
      <Card elevation={3} sx={{ width: '100%', maxWidth: 560, mx: 'auto' }}>
        <CardContent sx={{ p: { xs: 3, sm: 4 } }}>
          <Stack spacing={3}>
            <Stack spacing={1}>
              <Typography component="h1" variant="h4" sx={{ fontWeight: 500 }}>
                注册新账号
              </Typography>
              <Typography variant="body1" color="text.secondary">
                提交申请后由系统管理员审核；审核通过前不能登录。
              </Typography>
            </Stack>

            {submitted ? (
              <Stack spacing={2}>
                <Alert severity="success">
                  注册申请已提交（申请编号：{submitted.id}）。管理员审核通过后即可登录；
                  如果被拒绝，可以修改信息后重新提交。
                </Alert>
                <Button variant="contained" onClick={() => navigate('/login')}>
                  返回登录
                </Button>
              </Stack>
            ) : (
              <Box
                component="form"
                onSubmit={onSubmit}
                noValidate
                autoComplete="on"
                sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}
              >
                {serverError ? <Alert severity="error">{serverError}</Alert> : null}

                <TextField
                  {...field('username')}
                  label="用户名"
                  autoComplete="username"
                  autoFocus
                  fullWidth
                  helperText={errors.username?.message ?? '3–64 位，字母/数字/下划线/连字符'}
                  error={Boolean(errors.username)}
                />
                <TextField
                  {...field('email')}
                  label="邮箱"
                  type="email"
                  autoComplete="email"
                  fullWidth
                  helperText={errors.email?.message}
                  error={Boolean(errors.email)}
                />
                <TextField
                  {...field('display_name')}
                  label="显示名称"
                  autoComplete="name"
                  fullWidth
                  helperText={errors.display_name?.message ?? '在系统中展示的名称'}
                  error={Boolean(errors.display_name)}
                />
                <TextField
                  {...field('password')}
                  label="密码"
                  type="password"
                  autoComplete="new-password"
                  fullWidth
                  helperText={errors.password?.message ?? '至少 8 位'}
                  error={Boolean(errors.password)}
                />
                <TextField
                  {...field('confirm_password')}
                  label="确认密码"
                  type="password"
                  autoComplete="new-password"
                  fullWidth
                  helperText={errors.confirm_password?.message}
                  error={Boolean(errors.confirm_password)}
                />
                <TextField
                  {...field('timezone')}
                  label="时区（可选）"
                  placeholder="Asia/Shanghai"
                  fullWidth
                  helperText={errors.timezone?.message ?? 'IANA 时区；留空按 UTC'}
                  error={Boolean(errors.timezone)}
                />

                <Button
                  type="submit"
                  variant="contained"
                  size="large"
                  fullWidth
                  loading={isSubmitting}
                >
                  提交注册申请
                </Button>
                <Stack direction="row" sx={{ justifyContent: 'center' }}>
                  <Link component="button" type="button" variant="body2" onClick={() => navigate('/login')}>
                    已有账号？返回登录
                  </Link>
                </Stack>
              </Box>
            )}
          </Stack>
        </CardContent>
      </Card>
    </Box>
  );
}
