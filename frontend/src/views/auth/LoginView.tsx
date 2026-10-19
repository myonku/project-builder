/**
 * 登录页（来自官方 Sign-in 模板，已按本项目改造）。
 *
 * 相对模板的改动（累计，见 F04）：
 * - **删除**社交登录（Google / Facebook）：后端没有对应端点；
 * - 字段从 `email` 改为 `identifier`：`POST /auth/login` 接受**用户名或邮箱**（《A01》）；
 * - 接真实登录：`authStore.signIn` → 成功后按 `?redirect=` 回到原路径；
 * - `401 invalid_credentials` **不区分**"账号不存在"与"密码错误"（《S02》§2.4），
 *   因此统一提示"用户名/邮箱或密码不正确"；
 * - **样式改用 MUI 默认的 Material Design 2**（F04 D16）：不再自绘卡片阴影与放射状背景，
 *   直接用默认 `Card`；卡片加高、字号随主题上调（F04 D17）；
 * - 顶栏（品牌 + 明暗切换）由 `BlankLayout` 提供，登录页自己不再画；
 * - 新增"记住登录信息"勾选：**只把用户名存在本机浏览器**用于下次预填，
 *   并通过 `autoComplete` 告诉浏览器"可以保存这个站点的表单"。
 *   **它不延长会话**——会话有效期由服务端 Cookie / Redis 决定（《S02》§2.2），后端也没有"记住我"语义；
 * - "注册新账号"进入 `/register`（提交注册申请，管理员审核）；"忘记密码"仍是
 *   有明确反馈的占位入口（F04 D18）。
 *
 * 校验用 React Hook Form + Zod，只做**结构校验**，语义判定交给后端（F02 §9）。
 */

import * as React from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Checkbox from '@mui/material/Checkbox';
import FormControlLabel from '@mui/material/FormControlLabel';
import Link from '@mui/material/Link';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useForm } from 'react-hook-form';
import { useNavigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { toApiError } from '@/api/client';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import {
  STORAGE_KEY_LOGIN_IDENTIFIER,
  readStored,
  removeStored,
  writeStored,
} from '@/utils/storage';

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
  const { show } = useNotifications();
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [remember, setRemember] = React.useState(true);

  const redirectTo = searchParams.get('redirect') ?? '/';

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: {
      // 勾选"记住登录信息"时预填上次用过的用户名（密码永远不落本地存储）
      identifier: readStored(STORAGE_KEY_LOGIN_IDENTIFIER) ?? '',
      password: '',
    },
  });

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null);
    try {
      await signIn(values);
      if (remember) {
        writeStored(STORAGE_KEY_LOGIN_IDENTIFIER, values.identifier);
      } else {
        removeStored(STORAGE_KEY_LOGIN_IDENTIFIER);
      }
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
  const probeError = authError && !authError.isUnauthorized ? authError.message : null;

  /** 占位入口：不假装能跳转，也不毫无反应——直接说明现状与替代路径 */
  const notifyPlaceholder = React.useCallback(
    (title: string, detail: string) => {
      show(`${title}：${detail}`, { severity: 'info' });
    },
    [show],
  );

  return (
    <Box
      sx={{
        flex: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <Card
        elevation={3}
        sx={{
          width: '100%',
          maxWidth: 480,
          mx: 'auto',
        }}
      >
        <CardContent
          sx={{
            display: 'flex',
            flexDirection: 'column',
            gap: 3,
            // 卡片"拉长"：桌面端给一个明确的最小高度，内容在其中垂直居中
            minHeight: { xs: 'auto', sm: 600 },
            justifyContent: 'center',
            p: { xs: 3, sm: 4 },
          }}
        >
          <Stack spacing={1}>
            <Typography component="h1" variant="h4" sx={{ fontWeight: 500 }}>
              登录
            </Typography>
            <Typography variant="body1" color="text.secondary">
              项目/计划管理系统 · 仅限内部网络使用
            </Typography>
          </Stack>

          <Box
            component="form"
            onSubmit={onSubmit}
            noValidate
            // 勾选"记住登录信息"时示意浏览器可以保存本页表单；取消勾选则明确关掉
            autoComplete={remember ? 'on' : 'off'}
            sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}
          >
            {submitError ? <Alert severity="error">{submitError}</Alert> : null}
            {!submitError && probeError ? (
              <Alert severity="warning">{probeError}</Alert>
            ) : null}

            <TextField
              {...register('identifier')}
              id="identifier"
              label="用户名或邮箱"
              placeholder="username 或 your@email.com"
              autoComplete="username"
              autoFocus
              fullWidth
              error={Boolean(errors.identifier)}
              helperText={errors.identifier?.message}
            />
            <TextField
              {...register('password')}
              id="password"
              label="密码"
              type="password"
              placeholder="••••••••"
              autoComplete="current-password"
              fullWidth
              error={Boolean(errors.password)}
              helperText={errors.password?.message}
            />

            <FormControlLabel
              control={
                // 可访问名由 FormControlLabel 的 label 提供，不需要额外的 aria-label
                <Checkbox
                  checked={remember}
                  onChange={(event) => setRemember(event.target.checked)}
                />
              }
              label="记住登录信息"
              labelPlacement="start"
              slotProps={{ typography: { variant: 'body2' } }}
            />

            <Button
              type="submit"
              fullWidth
              variant="contained"
              size="large"
              loading={isSubmitting || status === 'loading'}
            >
              登录
            </Button>
          </Box>

          <Stack
            direction="row"
            spacing={2}
            sx={{ justifyContent: 'space-between', alignItems: 'center' }}
          >
            <Link
              component="button"
              type="button"
              variant="body2"
              onClick={() => navigate('/register')}
            >
              注册新账号
            </Link>
            <Link
              component="button"
              type="button"
              variant="body2"
              onClick={() =>
                notifyPlaceholder(
                  '忘记密码尚未实现',
                  '系统不提供自助找回密码，请联系管理员在「用户管理」中重置（POST /users/{id}/password）。',
                )
              }
            >
              忘记密码？
            </Link>
          </Stack>
        </CardContent>
      </Card>
    </Box>
  );
}
