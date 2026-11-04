import * as React from 'react';
import { Box, Button, Card, CardContent, LinearProgress, Pagination, Stack, Typography } from '@mui/material';
import { taskStatusMeta } from '@/components/task/taskMeta';
import type { PlanTask } from '@/types/plan';
import type { TaskStatus } from '@/types/task';
import { DUE_LABELS, STATUSES, dueBucket, selectedTasks, taskMetrics, type Selection } from './statisticsModel';

export function ChartCard({ title, children }: { title: string; children: React.ReactNode }) {
  return <Card variant="outlined" sx={{ minWidth: 0, breakInside: 'avoid' }}>
    <CardContent><Typography component="h2" variant="subtitle1" sx={{ mb: 2, fontWeight: 600 }}>{title}</Typography>{children}</CardContent>
  </Card>;
}
const colors: Record<TaskStatus, string> = { not_started: 'text.disabled', in_progress: 'primary.main', completed: 'success.main', cancelled: 'action.disabled' };

export function StatusChart({ tasks, onSelect, compact = false }: { tasks: readonly PlanTask[]; onSelect?: (selection: Selection) => void; compact?: boolean }) {
  const m = taskMetrics(tasks);
  if (!tasks.length) return <Typography color="text.secondary">无可见叶子任务，或当前筛选无结果。</Typography>;
  return <Stack spacing={1.5}>
    <Box role="img" aria-label={STATUSES.map(status => `${taskStatusMeta(status).label} ${m.status[status]}`).join('，')} title={STATUSES.map(status => `${taskStatusMeta(status).label} ${m.status[status]}`).join('，')} sx={{ display: 'flex', height: 22, borderRadius: 1, overflow: 'hidden' }}>
      {STATUSES.map(status => <Box key={status} sx={{ width: `${m.status[status] / m.total * 100}%`, bgcolor: colors[status] }} />)}
    </Box>
    {!compact && <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap', gap: 0.5 }}>
      {STATUSES.map(status => <Button key={status} size="small" color="inherit" disabled={!onSelect || !m.status[status]}
        onClick={() => onSelect?.({ kind: 'status', value: status })} sx={{ justifyContent: 'flex-start', color: 'text.primary', '&.Mui-disabled': { color: 'text.secondary' } }}>
        <Box component="span" sx={{ width: 9, height: 9, borderRadius: '50%', bgcolor: colors[status], mr: 0.75 }} />
        {taskStatusMeta(status).label} {m.status[status]}（{Math.round(m.status[status] / m.total * 100)}%）
      </Button>)}
    </Stack>}
    {!compact && <Typography variant="caption" color="text.secondary">共 {m.total} 个叶子任务 · 完成占比 {m.completion === null ? '—' : `${m.completion}%`}（不含取消）</Typography>}
  </Stack>;
}

export function DueChart({ tasks, today, onSelect }: { tasks: readonly PlanTask[]; today: string; onSelect: (selection: Selection) => void }) {
  const counts = DUE_LABELS.map((_, i) => tasks.filter(task => dueBucket(task, today) === i).length);
  const max = Math.max(1, ...counts);
  if (!taskMetrics(tasks).open) return <Typography color="text.secondary">当前没有未完成的叶子任务。</Typography>;
  return <Stack spacing={1}>
    {DUE_LABELS.map((label, i) => <Button key={label} color="inherit" onClick={() => onSelect({ kind: 'due', value: i })}
      disabled={!counts[i]} sx={{ display: 'grid', gridTemplateColumns: '100px minmax(20px, 1fr) 28px', gap: 1, textAlign: 'left', '&.Mui-disabled': { color: 'text.secondary' } }}>
      <Typography variant="body2">{label}</Typography>
      <LinearProgress variant="determinate" value={counts[i] / max * 100} color={i === 0 ? 'error' : 'primary'} sx={{ height: 12, borderRadius: 1 }} />
      <Typography variant="body2" sx={{ textAlign: 'right' }}>{counts[i]}</Typography>
    </Button>)}
  </Stack>;
}

export function MetricTiles({ tasks, projectCount }: { tasks: readonly PlanTask[]; projectCount: number }) {
  const m = taskMetrics(tasks);
  return <Box className="statistics-metrics" sx={{ display: 'grid', gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', md: 'repeat(5, minmax(0, 1fr))' }, gap: 1.5 }}>
    {[['涉及项目', projectCount], ['叶子任务', m.total], ['已完成', m.status.completed], ['未完成', m.open], ['逾期', m.overdue]].map(([label, value]) =>
      <Card variant="outlined" key={label}><CardContent sx={{ py: 1.5, '&:last-child': { pb: 1.5 } }}>
        <Typography color="text.secondary" variant="caption">{label}</Typography>
        <Typography variant="h5" color={label === '逾期' && m.overdue ? 'error.main' : 'text.primary'}>{value}</Typography>
      </CardContent></Card>)}
  </Box>;
}

export function TaskList({ tasks, onOpen }: { tasks: readonly PlanTask[]; onOpen: (id: string) => void }) {
  const [page, setPage] = React.useState(1);
  // Reset pagination when the caller changes its selection/scope by keying this component.
  const currentPage = Math.min(page, Math.max(1, Math.ceil(tasks.length / 10)));
  return <Stack spacing={1}>
    <Typography variant="body2" color="text.secondary">共 {tasks.length} 条，显示第 {tasks.length ? (currentPage - 1) * 10 + 1 : 0}–{Math.min(currentPage * 10, tasks.length)} 条</Typography>
    {!tasks.length && <Typography color="text.secondary">当前筛选无结果。</Typography>}
    {tasks.slice((currentPage - 1) * 10, currentPage * 10).map(task => <Button key={task.id} color="inherit" onClick={() => onOpen(task.id)}
      sx={{ textAlign: 'left', justifyContent: 'space-between', gap: 1, borderBottom: 1, borderColor: 'divider' }}>
      <Box sx={{ minWidth: 0, overflowWrap: 'anywhere' }}><Typography variant="body2">{task.name}</Typography>
        <Typography variant="caption" color="text.secondary">{task.end_date} 截止 · {taskStatusMeta(task.status).label}{task.is_overdue ? ' · 逾期' : ''}</Typography></Box>
      <Typography component="span" variant="caption" sx={{ flexShrink: 0 }}>查看</Typography>
    </Button>)}
    {tasks.length > 10 && <Pagination className="statistics-no-print" count={Math.ceil(tasks.length / 10)} page={currentPage} onChange={(_, value) => setPage(value)} size="small" />}
    <Typography variant="caption" color="text.secondary">明细只打印当前显示条目；其余条目可翻页查看。</Typography>
  </Stack>;
}

export function TaskOverview({ tasks, today, onOpen }: { tasks: readonly PlanTask[]; today: string; onOpen: (id: string) => void }) {
  const [selection, setSelection] = React.useState<Selection | null>(null);
  const m = taskMetrics(tasks);
  const dueSoon = tasks.filter(task => { const bucket = dueBucket(task, today); return bucket === 1 || bucket === 2; }).length;
  return <Stack spacing={1.5}>
    <StatusChart tasks={tasks} onSelect={setSelection} />
    <Typography variant="body2" color="text.secondary">逾期 {m.overdue} · 未来 7 天内到期（含今天）{dueSoon}</Typography>
    {selection && <><Button size="small" onClick={() => setSelection(null)}>清除选择 / 收起明细</Button>
      <TaskList key={JSON.stringify(selection)} tasks={selectedTasks(tasks, selection, today)} onOpen={onOpen} /></>}
  </Stack>;
}
