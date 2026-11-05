import * as React from 'react';
import { Alert, Box, Button, Stack, Typography } from '@mui/material';
import { Link as RouterLink } from 'react-router';
import { getOverview, type InsightQuery } from '@/api/insights';
import { useInsights } from '@/hooks/useInsights';
import { describeApiError } from '@/hooks/useApiError';
import { ChartCard, StatusChart, TaskList } from './StatisticsCharts';
import { ColumnChart } from './InsightCharts';
import { ActivityFeed } from './InsightOverview';
import { asPlanTasks, offsetDate } from './insightModel';
export default function ProjectInsightPanel({projectId, timeZone, revision, onOpen}: {projectId: string; timeZone?: string; revision: string; onOpen: (id: string) => void}) {
 const query: InsightQuery = {scope: 'project', project_id: projectId, timezone: timeZone ?? 'UTC', include_archived: true};
 const result = useInsights({...query, revision}, signal => getOverview(query, signal), timeZone);
 const [selection, setSelection] = React.useState<string | null>(null);
 const plan = asPlanTasks(result.data?.tasks ?? [], result.today);
 const points = Array.from({length: 14}, (_, i) => {const date = offsetDate(result.today, i);return {key: date, label: date.slice(5), value: plan.filter(task => task.end_date === date && (task.status === 'in_progress' || task.status === 'not_started')).length};});
 return <ChartCard title="可见叶子任务概况"><Stack spacing={2}>
  <Typography variant="caption" color="text.secondary">整个可见项目范围，不随下方任务表格筛选或分页变化。</Typography>
  {result.error && <Alert severity="warning" action={<Button onClick={result.reload}>重试</Button>}>{describeApiError(result.error)}</Alert>}
  {result.loading && <Typography>正在读取项目概况…</Typography>}
  {result.data && <><Box sx={{display: 'grid', gridTemplateColumns: {xs: 'minmax(0,1fr)', md: 'minmax(0,1fr) minmax(0,1.6fr)'}, gap: 2}}><StatusChart tasks={plan} onSelect={value => setSelection(String(value.value))} /><ColumnChart data={points} label="未完成到期" onSelect={i => setSelection(points[i].key!)} /></Box>
   <Typography>逾期 {result.data.overdue} · 未来7天内到期（含今天）{result.data.due_soon}</Typography>
   {selection && <><Button onClick={() => setSelection(null)}>清除图形选择</Button><TaskList key={selection} tasks={plan.filter(task => task.status === selection || (task.end_date === selection && (task.status === 'not_started' || task.status === 'in_progress')))} onOpen={onOpen} /></>}
   <Box component="details"><Box component="summary" sx={{cursor: 'pointer'}}>近期里程碑 / 最近变更</Box><Stack spacing={1} sx={{mt: 1}}>{result.data.milestones.slice(0, 3).map(task => <Button key={task.id} onClick={() => onOpen(task.id)}>{task.name} · {task.end_date}</Button>)}<ActivityFeed query={query} revision={Number(revision)} /></Stack></Box>
  </>}
  <Button component={RouterLink} to={`/gantt?view=projects&projectId=${projectId}`}>查看完整进度视图</Button>
 </Stack></ChartCard>;
}
