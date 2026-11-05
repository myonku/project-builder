import * as React from 'react';
import { useTheme } from '@mui/material/styles';
import { Box, Button, Card, CardContent, Pagination, Stack, Typography } from '@mui/material';
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
  const theme = useTheme();
  const palette = (theme.vars || theme).palette;
  const ringColors = [palette.text.disabled, palette.primary.main, palette.success.main, palette.action.disabled];
  const m = taskMetrics(tasks);
  if (!tasks.length) return <Typography color="text.secondary">无可见叶子任务，或当前筛选无结果。</Typography>;
  return <Stack spacing={1.5}>
    {compact ? <Box role="img" aria-label={STATUSES.map(status => `${taskStatusMeta(status).label} ${m.status[status]}`).join('，')} sx={{ display: 'flex', height: 22, borderRadius: 1, overflow: 'hidden' }}>
      {STATUSES.map(status => <Box key={status} sx={{ width: `${m.status[status] / m.total * 100}%`, bgcolor: colors[status] }} />)}
    </Box> : <Box sx={{ width: '100%', maxWidth: 250, mx: 'auto', position: 'relative' }}>
      <Box component="svg" viewBox="0 0 200 200" role="img" aria-label={STATUSES.map(status => `${taskStatusMeta(status).label} ${m.status[status]}`).join('，')} sx={{ width: '100%', display: 'block' }}>
        {STATUSES.map((status, index) => {
          const offset = STATUSES.slice(0, index).reduce((sum, s) => sum + m.status[s], 0) / m.total * 100;
          return <Box component="circle" key={status} cx="100" cy="100" r="76" fill="none" pathLength="100" strokeWidth="27"
            strokeDasharray={`${m.status[status] / m.total * 100} ${100 - m.status[status] / m.total * 100}`}
            strokeDashoffset={-offset} transform="rotate(-90 100 100)" stroke={ringColors[index]} />;
        })}
      </Box>
      <Stack sx={{ position: 'absolute', inset: '30%', justifyContent: 'center', alignItems: 'center', pointerEvents: 'none' }}>
        <Typography variant="h4">{m.total}</Typography><Typography variant="caption" color="text.secondary">叶子任务</Typography>
      </Stack>
    </Box>}
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
  return <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(5, minmax(0, 1fr))', gap: 1, height: 280, pt: 2 }}>
    {DUE_LABELS.map((label, i) => <Button key={label} color="inherit" onClick={() => onSelect({ kind: 'due', value: i })}
      disabled={!counts[i]} aria-label={`${label} ${counts[i]} 个任务，查看明细`}
      sx={{ minWidth: 0, px: 0.5, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', gap: 1, '&.Mui-disabled': { color: 'text.secondary' } }}>
      <Typography variant="body2">{counts[i]}</Typography>
      <Box sx={{ width: '65%', height: `${counts[i] / max * 170}px`, minHeight: counts[i] ? 3 : 0, bgcolor: i === 0 ? 'error.main' : 'primary.main', borderRadius: '4px 4px 0 0' }} />
      <Typography variant="caption" sx={{ minHeight: 42, textAlign: 'center' }}>{label}</Typography>
    </Button>)}
  </Box>;
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
