import * as React from 'react';
import { Alert, Box, Button, Stack, Typography } from '@mui/material';
import { Link as RouterLink } from 'react-router';
import { getActivity, getOverview, type InsightQuery, type InsightTask } from '@/api/insights';
import { useInsights } from '@/hooks/useInsights';
import { describeApiError } from '@/hooks/useApiError';
import { formatForDisplay } from '@/utils/datetime';
import { ChartCard, MetricTiles, StatusChart, TaskList } from './StatisticsCharts';
import { ColumnChart } from './InsightCharts';
import { asPlanTasks, offsetDate, topCategories } from './insightModel';
import { isOpen } from './statisticsModel';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';

const labels: Record<string, string> = {created: '创建', completed: '完成', reopened: '重新打开', cancelled: '取消', rescheduled: '调整日期', members: '调整成员', sharing: '调整共享', structure: '调整层级', restored: '恢复', updated: '更新', status: '调整状态'};
export function ActivityFeed({query, revision = 0}: {query: InsightQuery; revision?: number}) {
 return <ActivityFeedContent key={JSON.stringify([query, revision])} query={query} revision={revision} />;
}
function ActivityFeedContent({query, revision}: {query: InsightQuery; revision: number}) {
 const [cursor, setCursor] = React.useState('');
 const result = useInsights({...query, cursor, revision}, signal => getActivity(query, cursor, signal), query.timezone);
 return <Stack spacing={1}>
  {result.error && <Alert severity="warning">近期动态暂不可用：{describeApiError(result.error)}</Alert>}
  {result.loading && <Typography color="text.secondary">正在读取动态…</Typography>}
  {result.data?.items.map(event => <Box key={event.id} sx={{borderBottom: 1, borderColor: 'divider', pb: 1}}>
   <Typography variant="body2">{event.actor_name} · {labels[event.kind] ?? '更新'} · <Button component={RouterLink} to={`/projects/${event.after.project_id}?taskId=${event.after.id}`} size="small" sx={{textTransform: 'none', textAlign: 'left'}}>{event.after.name}</Button></Typography>
   {event.kind === 'rescheduled' && event.before && <Typography variant="caption">截止：{event.before.end_date} → {event.after.end_date}</Typography>}
   <Typography variant="caption" sx={{display: 'block'}} color="text.secondary">{event.after.project_name} · {formatForDisplay(event.occurred_at, query.timezone)}</Typography>
  </Box>)}
  {result.data && !result.data.items.length && <Typography color="text.secondary">暂无可见业务动态；历史从 {formatForDisplay(result.data.coverage.coverage_start, query.timezone)} 开始记录。</Typography>}
  {result.data?.next_cursor && <Button onClick={() => setCursor(result.data!.next_cursor)}>查看更多动态</Button>}
  {cursor && <Button onClick={() => setCursor('')}>返回最新动态</Button>}
 </Stack>;
}
export default function InsightOverview({query, compact = false, revision = 0, dueFrom = '', dueTo = ''}: {
 query: InsightQuery; compact?: boolean; revision?: number; dueFrom?: string; dueTo?: string;
}) {
 const result = useInsights({...query, revision}, signal => getOverview(query, signal), query.timezone);
 const [selected, setSelected] = React.useState<InsightTask[] | null>(null);
 const [taskId, setTaskId] = React.useState<string | null>(null);
 React.useEffect(() => {setSelected(null); setTaskId(null);}, [JSON.stringify(query), dueFrom, dueTo]);
 const data = result.data;
 const tasks = data?.tasks.filter(task => (!dueFrom || task.end_date >= dueFrom) && (!dueTo || task.end_date <= dueTo)) ?? [];
 const plan = asPlanTasks(tasks, result.today);
 const projectCount = new Set(tasks.map(task => task.project_id)).size;
 const categories = new Map<string, {label: string; key: string; value: number; second: number}>();
 for (const task of plan) {
  const members = query.scope === 'group' ? task.members : [{user_id: task.project_id, display_name: tasks.find(t => t.project_id === task.project_id)?.project_name ?? '项目'}];
  for (const member of members.length ? members : [{user_id: 'unassigned', display_name: '未分配'}]) {
   const row = categories.get(member.user_id) ?? {key: member.user_id, label: member.display_name, value: 0, second: 0};
   if (task.status === 'not_started') row.value++;
   if (task.status === 'in_progress') row.second++;
   categories.set(member.user_id, row);
  }
 }
 const rows = [...categories.values()].sort((a, b) => (b.value + b.second) - (a.value + a.second) || a.key.localeCompare(b.key));
 const chartRows = topCategories(rows, 8);
 const daily = Array.from({length: 14}, (_, i) => {const date = offsetDate(result.today, i);return {key: date, label: date.slice(5), value: plan.filter(t => isOpen(t) && t.end_date === date).length};});
 const sorted = [...plan].filter(isOpen).sort((a, b) => Number(b.is_overdue) - Number(a.is_overdue) || b.priority - a.priority || a.end_date.localeCompare(b.end_date));
 const active = tasks.find(task => task.id === taskId);
 return <Stack spacing={2}>
  <Stack direction="row" sx={{justifyContent: 'space-between', gap: 1, alignItems: 'center'}}>
   <Typography variant="caption" color="text.secondary">最近读取：{data ? formatForDisplay(data.as_of, query.timezone) : '—'} · {query.timezone}</Typography>
   <Button onClick={result.reload} disabled={result.loading}>刷新概览</Button>
  </Stack>
  {result.error && <Alert severity="error">{describeApiError(result.error)}</Alert>}
  {result.loading && <Typography>正在读取概览…</Typography>}
  {data && <>
   {compact ? <Box sx={{display: 'grid', gridTemplateColumns: {xs: 'repeat(2, minmax(0,1fr))', md: 'repeat(4,minmax(0,1fr))'}, gap: 1.5}}>
    {[['未完成', data.open], ['已逾期', data.overdue], ['未来7天到期（含今天）', data.due_soon], ['本周完成', data.coverage.complete ? data.period.completed : '记录不完整']].map(([label, value]) => <ChartCard key={label} title={String(label)}><Typography variant="h5">{value}</Typography></ChartCard>)}
   </Box> : <MetricTiles tasks={plan} projectCount={projectCount} />}
   <Box sx={{display: 'grid', gridTemplateColumns: {xs: 'minmax(0,1fr)', md: 'minmax(0, 1.6fr) minmax(0, 1fr)'}, gap: 2}}>
    <ChartCard title="未来14天到期安排"><ColumnChart data={daily} label="未完成任务" onSelect={i => setSelected(tasks.filter(task => task.end_date === daily[i].key && (task.status === 'not_started' || task.status === 'in_progress')))} />
     <Typography variant="caption" color="text.secondary">按当前截止日期统计，不代表历史产出。</Typography>
    </ChartCard>
    <ChartCard title="当前叶子任务状态"><StatusChart tasks={plan} onSelect={value => setSelected(tasks.filter(task => task.status === value.value))} /></ChartCard>
   </Box>
   {!compact && <ChartCard title={query.scope === 'group' ? '成员未完成任务分布' : '按项目的未完成任务分布'}>
    <ColumnChart data={chartRows} label="未开始" secondLabel="进行中" onSelect={i => {
     const keys = chartRows[i].key === 'other' ? new Set(rows.slice(7).map(row => row.key)) : new Set([chartRows[i].key]);
     setSelected(tasks.filter(task => query.scope === 'group' ? (task.members.length ? task.members.some(member => keys.has(member.user_id)) : keys.has('unassigned')) : keys.has(task.project_id)));
    }} />
    <Typography variant="caption">按统一数量标尺对比，其他分类已汇总。成员为参与任务数，多人任务可重复计入成员，不代表工时或效率。</Typography>
   </ChartCard>}
   {selected && <ChartCard title="图形对应明细"><Button onClick={() => setSelected(null)}>清除图形选择</Button><TaskList key={JSON.stringify(selected.map(t => t.id))} tasks={asPlanTasks(selected, result.today)} onOpen={setTaskId} /></ChartCard>}
   <Box sx={{display: 'grid', gridTemplateColumns: {xs: 'minmax(0,1fr)', md: 'repeat(2,minmax(0,1fr))'}, gap: 2}}>
    <ChartCard title="需要处理"><TaskList tasks={sorted.slice(0, 5)} onOpen={setTaskId} /><Typography variant="caption">未完成共 {sorted.length} 条，此处仅显示前 5 条。</Typography></ChartCard>
    <ChartCard title="近期动态"><ActivityFeed query={query} revision={Date.parse(data.as_of) + revision} /></ChartCard>
    <ChartCard title="近期里程碑"><Stack spacing={1}>{data.milestones.slice(0, 3).map(task => <Button key={task.id} onClick={() => setTaskId(task.id)} sx={{textAlign: 'left', justifyContent: 'flex-start'}}>{task.end_date < result.today ? '已逾期 · ' : ''}{task.name} · {task.end_date}</Button>)}{!data.milestones.length && <Typography color="text.secondary">未来30天及逾期范围暂无未完成里程碑。</Typography>}<Typography variant="caption">显示前 3 / 共 {data.milestones.length} 项</Typography></Stack></ChartCard>
    <ChartCard title="关注项目"><Stack spacing={1}>{[...new Set(sorted.map(t => t.project_id))].slice(0, 3).map(id => {
     const task = tasks.find(task => task.project_id === id)!;
     return <Button component={RouterLink} key={id} to={`/projects/${id}`} sx={{justifyContent: 'flex-start'}}>{task.project_name} · 逾期 {plan.filter(t => t.project_id === id && t.is_overdue).length}</Button>;
    })}{!sorted.length && <Typography color="text.secondary">当前没有含未完成任务的关注项目。</Typography>}</Stack></ChartCard>
   </Box>
   <Typography variant="caption" color="text.secondary">当前可见叶子任务；归属以任务成员为准。周期记录启用：{formatForDisplay(data.coverage.coverage_start, query.timezone)}。</Typography>
  </>}
  <TaskDetailDrawer key={active?.id ?? 'none'} taskId={active?.id ?? null} canEdit={active?.can_edit} onClose={() => setTaskId(null)} onChanged={() => {setTaskId(null);result.reload();}} />
 </Stack>;
}
