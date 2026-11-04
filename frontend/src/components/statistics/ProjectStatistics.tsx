import { Alert, Button, LinearProgress, Stack, Typography } from '@mui/material';
import { Link as RouterLink } from 'react-router';
import { ChartCard, TaskOverview } from './StatisticsCharts';
import { isOpen, leafTasks } from './statisticsModel';
import { useStatisticsSnapshot } from '@/hooks/useStatisticsSnapshot';
import { describeApiError } from '@/hooks/useApiError';
import { formatForDisplay } from '@/utils/datetime';

export default function ProjectStatistics({ projectId, timeZone, revision, onOpen }: {
  projectId: string; timeZone?: string; revision: string; onOpen: (id: string) => void;
}) {
  // The parent keys this component by the authoritative project revision after writes.
  const { payload, error, loading, reload, today, readAt } = useStatisticsSnapshot('project', projectId, false, timeZone);
  const mismatch = payload?.tasks.some(task => isOpen(task) && task.is_overdue !== (task.end_date < today));
  return <ChartCard title="可见叶子任务概况">
    <Stack spacing={1.5} data-project-revision={revision}>
      <Typography variant="caption" color="text.secondary">整个可见项目范围，不随下方任务表格分页、筛选或包含删除开关变化。</Typography>
      {loading && <LinearProgress aria-label="正在读取项目统计" />}
      {error && <Alert severity={error.isNotFound ? 'info' : 'error'} action={<Button onClick={reload}>重试</Button>}>{describeApiError(error)}</Alert>}
      {mismatch ? <Alert severity="warning" action={<Button onClick={reload}>刷新</Button>}>日期与逾期标记不一致，请检查设备日期并刷新核对。</Alert>
        : payload && <TaskOverview tasks={leafTasks(payload.tasks)} today={today} onOpen={onOpen} />}
      <Typography variant="caption" color="text.secondary">最近成功读取：{formatForDisplay(readAt, timeZone)} · {timeZone ?? 'UTC'}</Typography>
      <Button component={RouterLink} to={`/gantt?view=projects&projectId=${projectId}`}>查看完整进度视图</Button>
    </Stack>
  </ChartCard>;
}
