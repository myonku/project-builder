/**
 * 登录页（来自官方 Sign-in 模板，已按本项目改造）。
 *
 * 相对模板的改动：
 * - **删除**社交登录（Google / Facebook）、"记住我"、"忘记密码"、注册链接：
 *   后端没有对应端点，留着会让用户点了没反应（F03 §7 踩坑提示）；
 * - 字段从 `email` 改为 `identifier`：`POST /auth/login` 接受**用户名或邮箱**（《A01》）；
 * - 接真实登录：`authStore.signIn` → 成功后按 `?redirect=` 回到原路径；
 * - `401 invalid_credentials` **不区分**"账号不存在"与"密码错误"（《S02》§2.4），
 *   因此统一提示"用户名/邮箱或密码不正确"；
 * - 校验用 React Hook Form + Zod，只做**结构校验**，语义判定交给后端（F02 §9）。
 */

import * as React from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import FormControl from '@mui/material/FormControl';
import FormLabel from '@mui/material/FormLabel';
import MuiCard from '@mui/material/Card';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { styled } from '@mui/material/styles';
import { useForm } from 'react-hook-form';
import { useNavigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { toApiError } from '@/api/client';
import AppBrand from '@/components/common/AppBrand';
import ColorModeSelect from '@/theme/ColorModeSelect';
import { useAuthStore } from '@/stores/auth';

const Card = styled(MuiCard)(({ theme }) => ({
  display: 'flex',
  flexDirection: 'column',
  alignSelf: 'center',
  width: '100%',
  padding: theme.spacing(4),
  gap: theme.spacing(2),
  margin: 'auto',
  [theme.breakpoints.up('sm')]: {
    maxWidth: '450px',
  },
  boxShadow:
    'hsla(220, 30%, 5%, 0.05) 0px 5px 15px 0px, hsla(220, 25%, 10%, 0.05) 0px 15px 35px -5px',
  ...theme.applyStyles('dark', {
    boxShadow:
      'hsla(220, 30%, 5%, 0.5) 0px 5px 15px 0px, hsla(220, 25%, 10%, 0.08) 0px 15px 35px -5px',
  }),
}));

const SignInContainer = styled(Stack)(({ theme }) => ({
  minHeight: '100dvh',
  padding: theme.spacing(2),
  [theme.breakpoints.up('sm')]: {
    padding: theme.spacing(4),
  },
  '&::before': {
    content: '""',
    display: 'block',
    position: 'absolute',
    zIndex: -1,
    inset: 0,
    backgroundImage:
      'radial-gradient(ellipse at 50% 50%, hsl(210, 100%, 97%), hsl(0, 0%, 100%))',
    backgroundRepeat: 'no-repeat',
    ...theme.applyStyles('dark', {
      backgroundImage:
        'radial-gradient(at 50% 50%, hsla(210, 100%, 16%, 0.5), hsl(220, 30%, 5%))',
    }),
  },
}));

/** 只做结构校验：必填与类型，长度/强度等语义规则由后端判定（F02 §9） */
const loginSchema = z.object({
  identifier: z.string().min(1, '请输入用户名或邮箱'),
  password: z.string().min(1, '请输入密码'),
});

type LoginFormValues = z.infer<typeof loginSchema>;

export default function LoginView() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const signIn = useAuthStore((state) => state.signIn);
  const status = useAuthStore((state) => state.status);
  const authError = useAuthStore((state) => state.error);
  const [submitError, setSubmitError] = React.useState<string | null>(null);

  const redirectTo = searchParams.get('redirect') ?? '/';

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { identifier: '', password: '' },
  });

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null);
    try {
      await signIn(values);
      // 用 replace，避免用户按"后退"回到登录页
      navigate(redirectTo, { replace: true });
    } catch (error) {
      const apiError = toApiError(error);
      if (apiError.isUnauthorized) {
        // 后端刻意不区分"账号不存在"与"密码错误"
        setSubmitError('用户名/邮箱或密码不正确。');
      } else if (apiError.isForbidden) {
        setSubmitError('账号已被停用，请联系管理员。');
      } else if (apiError.isRateLimited) {
        setSubmitError(
          apiError.retryAfterSeconds
            ? `请求过于频繁，请 ${apiError.retryAfterSeconds} 秒后再试。`
            : '请求过于频繁，请稍后再试。',
        );
      } else {
        setSubmitError(apiError.message);
      }
    }
  });

  // 会话探测失败（例如后端没起）时也把原因显示出来，否则用户只看到"登录失败"
  const probeError =
    authError && !authError.isUnauthorized ? authError.message : null;

  return (
    <SignInContainer direction="column" sx={{ justifyContent: 'center' }}>
      <ColorModeSelect sx={{ position: 'fixed', top: '1rem', right: '1rem' }} />
      <Card variant="outlined">
        <AppBrand showTitle={false} />
        <Typography
          component="h1"
          variant="h4"
          sx={{ width: '100%', fontSize: 'clamp(1.75rem, 8vw, 2.15rem)' }}
        >
          登录
        </Typography>
        <Typography variant="body2" color="text.secondary">
          项目/计划管理系统 · 仅限内部网络使用
        </Typography>
        <Box
          component="form"
          onSubmit={onSubmit}
          noValidate
          sx={{ display: 'flex', flexDirection: 'column', width: '100%', gap: 2 }}
        >
          {submitError ? <Alert severity="error">{submitError}</Alert> : null}
          {!submitError && probeError ? (
            <Alert severity="warning">{probeError}</Alert>
          ) : null}
          <FormControl>
            <FormLabel htmlFor="identifier">用户名或邮箱</FormLabel>
            <TextField
              {...register('identifier')}
              id="identifier"
              type="text"
              placeholder="username 或 your@email.com"
              autoComplete="username"
              autoFocus
              required
              fullWidth
              variant="outlined"
              error={Boolean(errors.identifier)}
              helperText={errors.identifier?.message}
            />
          </FormControl>
          <FormControl>
            <FormLabel htmlFor="password">密码</FormLabel>
            <TextField
              {...register('password')}
              id="password"
              type="password"
              placeholder="••••••••"
              autoComplete="current-password"
              required
              fullWidth
              variant="outlined"
              error={Boolean(errors.password)}
              helperText={errors.password?.message}
            />
          </FormControl>
          <Button
            type="submit"
            fullWidth
            variant="contained"
            loading={isSubmitting || status === 'loading'}
          >
            登录
          </Button>
        </Box>
      </Card>
    </SignInContainer>
  );
}
