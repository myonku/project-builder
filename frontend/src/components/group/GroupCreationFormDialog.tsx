import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import { submitGroupCreation } from '@/api/groupCreation';
import { toApiError } from '@/api/client';
import { describeApiError } from '@/hooks/useApiError';
import type { CreateGroupApplication, GroupCreationRequest } from '@/types/groupCreation';

interface PendingSubmission { key: string; body: CreateGroupApplication }
export default function GroupCreationFormDialog({ open, userId, seed, onClose, onSubmitted }: {
  open: boolean; userId: string; seed?: GroupCreationRequest | null;
  onClose: () => void; onSubmitted: (result: GroupCreationRequest) => void;
}) {
  const storageKey = `worklane.group-creation.pending:${userId}`;
  const [name, setName] = React.useState('');
  const [description, setDescription] = React.useState('');
  const [reason, setReason] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [pending, setPending] = React.useState<PendingSubmission | null>(null);
  const [error, setError] = React.useState('');
  const [fields, setFields] = React.useState<Record<string, string>>({});
  const submitting = React.useRef(false);
  React.useEffect(() => {
    if (!open) return;
    let saved: PendingSubmission | null = null;
    try {
      const value = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as PendingSubmission | null;
      if (value && typeof value.key === 'string' && typeof value.body?.name === 'string' && typeof value.body?.reason === 'string') saved = value;
    } catch { /* unavailable storage does not prevent submitting */ }
    setPending(saved); setFields({}); setError('');
    setName(saved?.body.name ?? seed?.name ?? '');
    setDescription(saved?.body.description ?? seed?.description ?? '');
    setReason(saved?.body.reason ?? seed?.reason ?? '');
  }, [open, seed, storageKey]);
  async function submit(event: React.FormEvent) {
    event.preventDefault(); if (submitting.current) return;
    const body: CreateGroupApplication = { name: name.trim(), reason: reason.trim(), ...(description.trim() ? { description: description.trim() } : {}) };
    const next: Record<string, string> = {};
    if (!body.name || Array.from(body.name).length > 160) next.name = '工作组名称须为 1–160 个字符';
    if (Array.from(body.description ?? '').length > 2000) next.description = '说明不超过 2000 个字符';
    if (!body.reason || Array.from(body.reason).length > 2000) next.reason = '申请理由须为 1–2000 个字符';
    setFields(next); if (Object.keys(next).length) return;
    const submission = pending ?? { key: crypto.randomUUID(), body };
    submitting.current = true; setBusy(true); setError('');
    try { sessionStorage.setItem(storageKey, JSON.stringify(submission)); } catch { /* keep the same key in memory */ }
    try {
      const result = await submitGroupCreation(submission.body, submission.key);
      try { sessionStorage.removeItem(storageKey); } catch { /* no persisted copy */ }
      setPending(null); onSubmitted(result);
    } catch (caught) {
      const apiError = toApiError(caught);
      setError(describeApiError(apiError)); setFields(apiError.fields);
      if (apiError.status === 0 || apiError.status >= 500 || apiError.status === 401) {
        setPending(submission);
      } else {
        setPending(null); try { sessionStorage.removeItem(storageKey); } catch { /* no persisted copy */ }
      }
    } finally { submitting.current = false; setBusy(false); }
  }
  return <Dialog open={open} onClose={() => { if (!busy) onClose(); }} fullWidth maxWidth="sm" aria-labelledby="group-creation-form-title">
    <DialogTitle id="group-creation-form-title">申请新建工作组</DialogTitle>
    <form onSubmit={event => void submit(event)}>
      <DialogContent><Stack spacing={2}>
        <Alert severity="info">系统管理员审核通过后创建，你将成为工作组所有者。</Alert>
        {error && <Alert severity="error">{error}</Alert>}
        {pending && <Alert severity="warning">提交结果待核对。请使用原请求重试；关闭窗口后再次打开也会保留本次请求。</Alert>}
        <TextField label="工作组名称" autoFocus value={name} onChange={e => setName(e.target.value)} disabled={busy || !!pending} error={!!fields.name} helperText={fields.name} fullWidth />
        <TextField label="说明" multiline minRows={2} value={description} onChange={e => setDescription(e.target.value)} disabled={busy || !!pending} error={!!fields.description} helperText={fields.description ?? '选填，最多 2000 个字符'} fullWidth />
        <TextField label="申请理由" multiline minRows={3} value={reason} onChange={e => setReason(e.target.value)} disabled={busy || !!pending} error={!!fields.reason} helperText={fields.reason ?? '请说明工作组的用途，最多 2000 个字符'} fullWidth />
      </Stack></DialogContent>
      <DialogActions><Button onClick={onClose} disabled={busy}>关闭</Button><Button type="submit" variant="contained" disabled={busy}>{busy ? '提交中…' : pending ? '核对并重试' : '提交申请'}</Button></DialogActions>
    </form>
  </Dialog>;
}
