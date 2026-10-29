import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Slider from '@mui/material/Slider';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import dayjs from 'dayjs';
import type { GanttEditController } from '@/hooks/useGanttEdit';
import { changedGanttDraft, progressStatus, validGanttDraft } from './ganttEditModel';

export default function GanttEditDialog({ edit, projectName }: { edit: GanttEditController; projectName: string }) {
  const draft = edit.draft;
  const open = Boolean(draft && ['confirming', 'saving', 'refreshing'].includes(edit.phase));
  const pending = edit.phase === 'saving' || edit.phase === 'refreshing';
  if (!draft) return null;
  const progress = draft.operation === 'progress';
  return <Dialog open={open} fullWidth maxWidth="sm"
    onClose={() => { if (!pending) edit.cancel(); }} aria-labelledby="gantt-edit-title"
    disableRestoreFocus>
    <DialogTitle id="gantt-edit-title">{progress ? '确认修改进度' : '确认修改日期'}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 0.5 }}>
        <Typography variant="body2" color="text.secondary">项目：{projectName} · 任务：{draft.task.name}</Typography>
        {progress ? <>
          <Typography>{draft.task.progress}% → {Number.isFinite(draft.progress) ? draft.progress : '—'}%</Typography>
          <Slider aria-label="任务进度" min={0} max={100} step={1} valueLabelDisplay="auto" disabled={pending}
            value={Number.isFinite(draft.progress) ? draft.progress : draft.task.progress}
            onChange={(_, value) => edit.change({ progress: value as number })} />
          <TextField label="进度百分比" type="number" value={Number.isFinite(draft.progress) ? draft.progress : ''}
            disabled={pending} slotProps={{ htmlInput: { min: 0, max: 100, step: 1 } }}
            onChange={(event) => edit.change({ progress: event.target.value === '' ? NaN : Number(event.target.value) })} />
          <Alert severity="info">目标状态：{validGanttDraft(draft) ? progressStatus(draft.progress) : '请输入 0–100 整数'}。进度下降可能重新激活已完成任务及其提醒；保存后可能更新父任务和项目的汇总状态/进度。</Alert>
        </> : <>
          <Typography variant="body2">原日期：{draft.task.start_date} ～ {draft.task.end_date}<br />新日期：{draft.start || '—'} ～ {draft.end || '—'}
            {draft.operation === 'move' && validGanttDraft(draft) ? `（平移 ${dayjs(draft.start).diff(dayjs(draft.task.start_date), 'day')} 天）` : ''}</Typography>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
            <TextField label="开始日期" type="date" value={draft.start} disabled={pending} fullWidth
              slotProps={{ inputLabel: { shrink: true } }} onChange={(event) => edit.change({ start: event.target.value })} />
            <TextField label="截止日期" type="date" value={draft.end} disabled={pending} fullWidth
              slotProps={{ inputLabel: { shrink: true } }} onChange={(event) => edit.change({ end: event.target.value })} />
          </Stack>
          <Typography variant="body2" color="text.secondary">开始不得晚于截止。日期需满足父子区间约束；修改父任务不会自动移动子任务，最终以服务端校验为准。</Typography>
        </>}
        <Typography variant="body2" color="text.secondary">保存后将同步相关提醒计划；已获发送授权的邮件可能仍按原内容发送。</Typography>
        {!validGanttDraft(draft) ? <Alert severity="warning">{progress ? '进度必须为 0–100 整数。' : '请输入有效日期，且开始不得晚于截止。'}</Alert> : null}
        {edit.error ? <Alert severity="error">{edit.error}</Alert> : null}
        {pending ? <Alert severity="info">{edit.phase === 'saving' ? '保存中…' : '已保存，正在刷新视图…'}</Alert> : null}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button disabled={pending} onClick={edit.cancel}>取消</Button>
      <Button variant="contained" disabled={pending || !validGanttDraft(draft) || !changedGanttDraft(draft)}
        onKeyDown={(event) => { if (event.repeat) event.preventDefault(); }} onClick={() => void edit.save()}>
        {pending ? '处理中…' : '确认修改'}
      </Button>
    </DialogActions>
  </Dialog>;
}
