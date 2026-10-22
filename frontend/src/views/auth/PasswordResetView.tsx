import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Link from '@mui/material/Link';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { Link as RouterLink } from 'react-router';
import { confirmPasswordReset, requestPasswordReset } from '@/api/auth';
import { toApiError } from '@/api/client';
import AppBrand from '@/components/common/AppBrand';

export default function PasswordResetView({ confirm = false }: { confirm?: boolean }) {
  // Keep the secret in component memory and remove it from browser history immediately.
  const [token, setToken] = React.useState(() => new URLSearchParams(window.location.search).get('token') ?? '');
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [repeat, setRepeat] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [done, setDone] = React.useState(false);
  const [error, setError] = React.useState('');
  const [fields, setFields] = React.useState<Record<string, string>>({});
  React.useLayoutEffect(() => {
    if (confirm && window.location.search) {
      window.history.replaceState(window.history.state, '', window.location.pathname);
    }
  }, [confirm]);
  const validToken = /^[0-9a-f]{64}$/i.test(token);
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    setError(''); setFields({});
    if (confirm && password !== repeat) { setFields({ repeat: '两次输入的密码不一致' }); return; }
    setBusy(true);
    try {
      if (confirm) {
        await confirmPasswordReset({ token, new_password: password });
        setPassword(''); setRepeat(''); setToken('');
      } else {
        await requestPasswordReset({ email: email.trim() });
      }
      setDone(true);
    } catch (cause) {
      const apiError = toApiError(cause);
      setFields(apiError.fields);
      if (apiError.code === 'password_reset_token_invalid') setError('重置链接已失效，请重新申请。');
      else if (apiError.code === 'password_reset_unavailable') setError('密码找回暂不可用，请联系管理员或稍后重试。');
      else if (apiError.isRateLimited) setError(`请求过于频繁，请在 ${apiError.retryAfterSeconds ?? 60} 秒后重试。`);
      else if (Object.keys(apiError.fields).length === 0) setError('提交失败，请稍后重试。');
    } finally { setBusy(false); }
  }
  return (
    <Card sx={{ width: '100%', maxWidth: 450, mx: 'auto', my: 4 }}>
      <CardContent sx={{ p: 4 }}>
        <Stack spacing={3}>
          <AppBrand />
          <Typography component="h1" variant="h5">{confirm ? '设置新密码' : '找回密码'}</Typography>
          {done ? <Alert severity="success" role="status">{confirm ? '密码已重置，请使用新密码重新登录。' : '如果该邮箱符合找回条件，已发送重置邮件。请检查收件箱；链接 30 分钟内有效。'}</Alert> : (
            <Stack component="form" spacing={2} onSubmit={submit}>
              {error && <Alert severity="error">{error}</Alert>}
              {confirm ? <>
                {!validToken && <Alert severity="warning">重置链接无效，请重新申请。</Alert>}
                <TextField required label="新密码" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} error={!!fields.new_password} helperText={fields.new_password ?? '至少 8 位，最多 256 个字符'} slotProps={{ htmlInput: { minLength: 8, maxLength: 256 } }} />
                <TextField required label="确认新密码" type="password" autoComplete="new-password" value={repeat} onChange={e => setRepeat(e.target.value)} error={!!fields.repeat} helperText={fields.repeat} />
                {fields.token && <Alert severity="error">{fields.token}</Alert>}
              </> : <>
                <Typography variant="body2" color="text.secondary">输入账号邮箱，我们将发送密码重置链接。</Typography>
                <TextField required label="邮箱" type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} error={!!fields.email} helperText={fields.email} />
              </>}
              <Button type="submit" variant="contained" disabled={busy || (confirm && !validToken)}>{busy ? '提交中…' : confirm ? '重置密码' : '发送重置邮件'}</Button>
            </Stack>
          )}
          <Stack direction="row" sx={{ justifyContent: 'space-between' }}>
            <Link component={RouterLink} to="/login">返回登录</Link>
            {confirm && <Link component={RouterLink} to="/forgot-password">重新申请链接</Link>}
          </Stack>
        </Stack>
      </CardContent>
    </Card>
  );
}
