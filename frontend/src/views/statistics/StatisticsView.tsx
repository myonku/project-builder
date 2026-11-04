import * as React from 'react';
import { Alert, Box, Button, Checkbox, FormControlLabel, LinearProgress, MenuItem, Stack, Tab, Tabs, TextField, Typography } from '@mui/material';
import { Link as RouterLink, useSearchParams } from 'react-router';
import PageContainer from '@/components/common/PageContainer';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';
import { ChartCard, DueChart, MetricTiles, StatusChart, TaskList } from '@/components/statistics/StatisticsCharts';
import { DUE_LABELS, isOpen, isUUID, memberDistribution, reportText, selectedTasks, statisticsScope, taskMetrics, validDate, type Selection } from '@/components/statistics/statisticsModel';
import { taskStatusMeta } from '@/components/task/taskMeta';
import { useStatisticsSnapshot } from '@/hooks/useStatisticsSnapshot';
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { describeApiError } from '@/hooks/useApiError';
import { useAuthStore } from '@/stores/auth';
import { useUiStore } from '@/stores/ui';
import { formatForDisplay } from '@/utils/datetime';

export default function StatisticsView() {
  const [params, setParams] = useSearchParams();
  const timezone = useAuthStore(state => state.user?.timezone) ?? 'UTC';
  const currentGroupId = useUiStore(state => state.currentGroupId);
  const { options: groups, error: groupsError, loading: groupsLoading } = useGroupOptions();
  const groupId = params.get('groupId') ?? '';
  const projectId = params.get('projectId') ?? '';
  const memberId = params.get('memberId') ?? '';
  const from = params.get('dueFrom') ?? '';
  const to = params.get('dueTo') ?? '';
  const tab = params.get('tab') ?? 'group';
  const archived = params.get('includeArchived') === 'true';
  const validGroup = isUUID(groupId);
  const invalid = (!!groupId && !validGroup) || (!!projectId && !isUUID(projectId))
    || (!!memberId && memberId !== 'unassigned' && !isUUID(memberId))
    || (!!from && !validDate(from)) || (!!to && !validDate(to)) || (!!from && !!to && from > to)
    || !['group', 'members'].includes(tab)
    || (params.has('includeArchived') && !['true', 'false'].includes(params.get('includeArchived')!));
  const snapshot = useStatisticsSnapshot('group', validGroup ? groupId : null, archived, timezone);
  const { payload, loading, error, reload, today, readAt } = snapshot;
  const [selection, setSelection] = React.useState<Selection | null>(null);
  const [showAll, setShowAll] = React.useState(false);
  const [detailOpen, setDetailOpen] = React.useState(false);
  const [search, setSearch] = React.useState('');
  const [notice, setNotice] = React.useState('');
  const [taskId, setTaskId] = React.useState<string | null>(null);
  const scopeKey = JSON.stringify([groupId, projectId, from, to, tab, memberId, archived]);
  React.useEffect(() => { setSelection(null); setShowAll(false); setDetailOpen(false); setSearch(''); setNotice(''); setTaskId(null); }, [scopeKey]);
  React.useEffect(() => {
    if (!params.has('groupId') && groups.length) {
      const next = new URLSearchParams(params);
      next.set('groupId', groups.find(group => group.id === currentGroupId)?.id ?? groups[0].id);
      setParams(next, { replace: true });
    }
  }, [groups, params, currentGroupId, setParams]);
  const update = (values: Record<string, string>) => {
    const next = new URLSearchParams(params);
    // Only UI parameters are serialized. None are blindly forwarded to the API.
    for (const key of [...next.keys()]) if (!['groupId', 'projectId', 'memberId', 'dueFrom', 'dueTo', 'tab', 'includeArchived'].includes(key)) next.delete(key);
    Object.entries(values).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key));
    setParams(next);
  };
  const scope = React.useMemo(() => statisticsScope(payload?.projects ?? [], payload?.tasks ?? [], projectId, from, to), [payload, projectId, from, to]);
  const members = React.useMemo(() => memberDistribution(scope.leaves, scope.tasks), [scope]);
  const member = members.find(row => row.member.user_id === memberId);
  const missingProject = !!payload && !!projectId && !payload.projects.some(project => project.id === projectId);
  const missingMember = tab === 'members' && !!memberId && memberId !== 'unassigned' && !member;
  const unassigned = scope.tasks.filter(task => task.members.length === 0);
  const memberTasks = memberId === 'unassigned' ? unassigned : member?.tasks ?? [];
  const focusTasks = tab === 'members' && memberId ? memberTasks : scope.tasks;
  const memberName = memberId === 'unassigned' ? '未分配' : member ? `${member.member.display_name}（${member.member.username}）` : '';
  const groupName = groups.find(group => group.id === groupId)?.name ?? groupId;
  const projectName = payload?.projects.find(project => project.id === projectId)?.name ?? '全部项目';
  const scopeText = `${groupName} · ${projectName} · 截止 ${from || '不限'} 至 ${to || '不限'} · ${archived ? '含归档' : '不含归档'} · ${timezone}${tab === 'members' && memberId ? ` · 成员：${memberName}` : ''}`;
  const mismatch = scope.tasks.some(task => isOpen(task) && task.is_overdue !== (task.end_date < today));
  const ready = !!payload && !loading && !error && !invalid && !missingProject && !missingMember && !mismatch;
  const projectMetrics = React.useMemo(() => {
    const counts = new Map<string, { total: number; open: number; overdue: number }>();
    for (const task of scope.tasks) {
      const value = counts.get(task.project_id) ?? { total: 0, open: 0, overdue: 0 };
      value.total++;
      if (isOpen(task)) value.open++;
      if (isOpen(task) && task.is_overdue) value.overdue++;
      counts.set(task.project_id, value);
    }
    return counts;
  }, [scope]);
  const projects = [...scope.projects].sort((a, b) => {
    return (projectMetrics.get(b.id)?.overdue ?? 0) - (projectMetrics.get(a.id)?.overdue ?? 0) || a.end_date.localeCompare(b.end_date) || a.id.localeCompare(b.id);
  });
  const searchedMembers = members.filter(row => `${row.member.display_name} ${row.member.username}`.toLocaleLowerCase().includes(search.toLocaleLowerCase()));
  const detailTasks = selectedTasks(focusTasks, selection, today).sort((a, b) => a.end_date.localeCompare(b.end_date) || a.id.localeCompare(b.id));
  const select = (next: Selection) => { setSelection(next); setDetailOpen(true); };
  const activeTask = ready ? payload?.tasks.find(task => task.id === taskId) : undefined;
  const readLabel = formatForDisplay(readAt, timezone);
  const reportProjectCount = tab === 'members' && memberId ? new Set(focusTasks.map(task => task.project_id)).size : scope.projects.length;
  const copy = async () => {
    try {
      const ids = new Set(focusTasks.map(task => task.project_id));
      const reportProjects = tab === 'members' && memberId ? projects.filter(project => ids.has(project.id)) : projects;
      const reportMembers = memberId ? members.filter(row => row.member.user_id === memberId) : members;
      const notes = [
        `项目当前进度（前 ${Math.min(5, reportProjects.length)} / 共 ${reportProjects.length}）：${reportProjects.slice(0, 5).map(project => `${project.name} ${project.progress}%`).join('；') || '无'}`,
        `成员参与分布（前 ${Math.min(5, reportMembers.length)} / 共 ${reportMembers.length}）：${reportMembers.slice(0, 5).map(row => `${row.member.display_name}（${row.member.username}）参与 ${row.tasks.length}，逾期 ${taskMetrics(row.tasks).overdue}`).join('；') || '无'}`,
      ];
      await navigator.clipboard.writeText(reportText(scopeText, focusTasks, reportProjectCount, `${readLabel} (${timezone})`, today, window.location.origin, notes));
      setNotice('已复制当前范围摘要。');
    } catch { setNotice('复制失败，请允许剪贴板访问，或使用打印保存汇报。'); }
  };
  return <PageContainer title="数据统计" breadcrumbs={[{ title: '工作台', path: '/' }, { title: '数据统计' }]}>
    <Stack className="statistics-page" spacing={2}>
      <Stack className="statistics-no-print" direction="row" useFlexGap sx={{ flexWrap: 'wrap', gap: 1 }}>
        <Button variant="outlined" onClick={reload} disabled={!validGroup || loading}>刷新</Button>
        <Button variant="outlined" onClick={() => void copy()} disabled={!ready}>复制摘要</Button>
        <Button variant="outlined" onClick={() => window.print()} disabled={!ready}>打印汇报</Button>
        <Button component={RouterLink} to="/plans/group" sx={{ ml: 'auto' }}>组内计划</Button>
      </Stack>
      <Typography variant="body2" color="text.secondary">最近成功读取：{ready ? readLabel : '—'} · {timezone}</Typography>
      <Tabs className="statistics-no-print" value={tab === 'members' ? 'members' : 'group'} onChange={(_, value) => update({ tab: value, memberId: '' })}>
        <Tab value="group" label="组内概况" /><Tab value="members" label="成员分布" />
      </Tabs>
      <Box className="statistics-no-print" sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: 'repeat(2, minmax(0, 1fr))', lg: 'repeat(4, minmax(0, 1fr))' } }}>
        <TextField select label="工作组" size="small" value={groupId} onChange={event => update({ groupId: event.target.value, projectId: '', memberId: '' })} disabled={groupsLoading}>
          {!groups.some(group => group.id === groupId) && <MenuItem value={groupId}>{groupId ? `指定工作组 ${groupId}` : '请选择工作组'}</MenuItem>}
          {groups.map(group => <MenuItem key={group.id} value={group.id}>{group.name}</MenuItem>)}
        </TextField>
        <TextField select label="项目" size="small" value={projectId} onChange={event => update({ projectId: event.target.value, memberId: '' })} disabled={!payload || loading} slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}>
          <MenuItem value="">全部项目</MenuItem>
          {missingProject && <MenuItem value={projectId}>所选项目不可用</MenuItem>}
          {payload?.projects.map(project => <MenuItem key={project.id} value={project.id}>{project.name}</MenuItem>)}
        </TextField>
        <TextField type="date" label="截止日期从" size="small" value={from} onChange={event => update({ dueFrom: event.target.value })} slotProps={{ inputLabel: { shrink: true } }} />
        <TextField type="date" label="截止日期至" size="small" value={to} onChange={event => update({ dueTo: event.target.value })} slotProps={{ inputLabel: { shrink: true } }} />
      </Box>
      <Stack className="statistics-no-print" direction="row" useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
        <FormControlLabel control={<Checkbox checked={archived} onChange={(_, checked) => update({ includeArchived: String(checked), memberId: '' })} />} label="纳入归档" />
        <Button size="small" onClick={() => update({ projectId: '', memberId: '', dueFrom: '', dueTo: '', includeArchived: '' })}>重置筛选</Button>
      </Stack>
      {notice && <Alert className="statistics-no-print" severity={notice.startsWith('已') ? 'success' : 'warning'}>{notice}</Alert>}
      {groupsError && <Alert severity="warning">工作组选项读取失败：{describeApiError(groupsError)}</Alert>}
      {!groupId && !groupsLoading && <Alert severity="info">当前没有可选工作组。加入工作组后可查看共享计划统计。</Alert>}
      {invalid && <Alert severity="warning">查询条件无效，请检查工作组、成员、截止日期范围或页签参数。</Alert>}
      {loading && <LinearProgress aria-label="正在读取统计数据" />}
      {error && <Alert severity={error.isNotFound ? 'info' : 'error'}>统计范围不可用：{describeApiError(error)}</Alert>}
      {missingProject && <Alert severity="info">所选项目不在当前共享范围内。请重新选择项目。</Alert>}
      {missingMember && <Alert severity="info">所选成员不在当前项目的可见叶子任务范围内。<Button onClick={() => update({ memberId: '' })}>清除成员</Button></Alert>}
      {mismatch && <Alert severity="warning">日期与逾期标记不一致，请检查设备日期并刷新核对。</Alert>}
      {ready && <>
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{tab === 'members' ? '成员分布' : '组内概况'} · {scopeText}</Typography>
        <Typography variant="caption" color="text.secondary">本组共享计划 · 可见叶子任务 · 当前状态，非历史完成趋势。项目进度由完整任务层级汇总，截止范围仅决定展示哪些项目。</Typography>
        <MetricTiles tasks={scope.tasks} projectCount={scope.projects.length} />
        <Typography variant="caption" color="text.secondary">以上为组级范围；成员详情及汇报摘要按下方选择的成员计算。</Typography>
        {tab === 'group' ? <>
          <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: 'repeat(2, minmax(0, 1fr))' }, gap: 2 }}>
            <ChartCard title="叶子任务状态构成"><StatusChart tasks={scope.tasks} onSelect={select} /></ChartCard>
            <ChartCard title="未完成任务到期分布"><DueChart tasks={scope.tasks} today={today} onSelect={select} /></ChartCard>
          </Box>
          <ChartCard title="项目当前进度对比">
            <Stack spacing={1.5}>
              <Typography variant="caption" color="text.secondary">显示 {Math.min(showAll ? projects.length : 8, projects.length)} / 共 {projects.length} 个项目 · 按本次逾期叶子任务数、截止日期排序</Typography>
              {!projects.length && <Typography color="text.secondary">当前筛选无结果。</Typography>}
              {(showAll ? projects : projects.slice(0, 8)).map(project => {
                const metrics = projectMetrics.get(project.id) ?? { total: 0, open: 0, overdue: 0 };
                return <Box key={project.id}>
                  <Stack direction="row" sx={{ justifyContent: 'space-between', gap: 1 }}><Button component={RouterLink} to={`/projects/${project.id}`} color="inherit" sx={{ minWidth: 0, overflowWrap: 'anywhere', textAlign: 'left', justifyContent: 'flex-start' }}>{project.name}</Button><Typography sx={{ alignSelf: 'center' }}>{project.progress}%</Typography></Stack>
                  <LinearProgress variant="determinate" value={project.progress} sx={{ height: 8, borderRadius: 1 }} />
                  <Typography variant="caption" color="text.secondary">未完成 {metrics.open} · 逾期 {metrics.overdue}{!metrics.total ? ' · 无可见叶子任务' : ''}{project.is_archived ? ' · 已归档' : ''}{project.status === 'cancelled' ? ' · 已取消' : ''}</Typography>
                </Box>;
              })}
              {projects.length > 8 && <Button className="statistics-no-print" onClick={() => setShowAll(value => !value)}>{showAll ? '收起项目' : '展开全部项目'}</Button>}
            </Stack>
          </ChartCard>
        </> : <>
          <ChartCard title="成员任务分布">
            <Stack spacing={1.5}>
              <Typography variant="caption" color="text.secondary">参与任务数；多人任务分别计入成员，合计不等于组级任务数。仅展示可见叶子任务中的成员，不代表工时或效率。</Typography>
              <TextField className="statistics-no-print" size="small" label="查找成员姓名 / 用户名" value={search} onChange={event => setSearch(event.target.value)} />
              <Typography variant="caption">{search ? `查找「${search}」 · ` : ''}显示 {Math.min(showAll ? searchedMembers.length : 10, searchedMembers.length)} / 共 {searchedMembers.length} 位匹配成员（范围内共 {members.length} 位）</Typography>
              {(showAll ? searchedMembers : searchedMembers.slice(0, 10)).map(row => {
                const metrics = taskMetrics(row.tasks);
                return <Box key={row.member.user_id}>
                  <Button color="inherit" aria-pressed={memberId === row.member.user_id} onClick={() => update({ memberId: row.member.user_id })} sx={{ textAlign: 'left', overflowWrap: 'anywhere' }}>
                    {row.member.display_name}（{row.member.username}） · 参与 {metrics.total} · 未完成 {metrics.open} · 逾期 {metrics.overdue}
                  </Button>
                  <StatusChart tasks={row.tasks} compact />
                </Box>;
              })}
              {!searchedMembers.length && <Typography color="text.secondary">当前没有匹配成员。</Typography>}
              {searchedMembers.length > 10 && <Button className="statistics-no-print" onClick={() => setShowAll(value => !value)}>{showAll ? '收起成员' : '展开全部成员'}</Button>}
              <Button color="inherit" onClick={() => update({ memberId: 'unassigned' })}>未分配：{unassigned.length} 个叶子任务</Button>
            </Stack>
          </ChartCard>
          {memberId ? <>
            <Typography component="h2" variant="h6">{memberName} · 参与任务详情（涉及项目 {reportProjectCount}）</Typography>
            <Button className="statistics-no-print" onClick={() => update({ memberId: '' })}>清除成员选择</Button>
            <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: 'repeat(2, minmax(0, 1fr))' }, gap: 2 }}>
              <ChartCard title="成员叶子任务状态"><StatusChart tasks={memberTasks} onSelect={select} /></ChartCard>
              <ChartCard title="成员未完成任务到期分布"><DueChart tasks={memberTasks} today={today} onSelect={select} /></ChartCard>
            </Box>
          </> : <Alert severity="info">选择成员查看详情；当前摘要按全组范围生成。</Alert>}
        </>}
        <ChartCard title={selection ? `任务明细 · ${selection.kind === 'status' ? taskStatusMeta(selection.value).label : DUE_LABELS[selection.value]}` : '需关注任务与明细'}>
          <Stack spacing={1}>
            {selection && <Button className="statistics-no-print" onClick={() => setSelection(null)}>清除图形选择</Button>}
            <Button className="statistics-no-print" onClick={() => setDetailOpen(value => !value)}>{detailOpen ? '收起明细' : '查看完整明细'}</Button>
            <TaskList key={`${scopeKey}:${JSON.stringify(selection)}:${detailOpen}`} tasks={detailOpen ? detailTasks : detailTasks.filter(isOpen).slice(0, 5)} onOpen={setTaskId} />
            {!detailOpen && <Typography variant="caption">仅显示需关注任务前 5 条（共 {detailTasks.filter(isOpen).length} 条未完成任务）；展开可核查全部 {detailTasks.length} 条。</Typography>}
          </Stack>
        </ChartCard>
        <Typography variant="caption" color="text.secondary">本页为实时查询，读取时间不代表数据库统一快照。共享链接按接收者权限重新读取；打印仅包含当前已展示的项目、成员及明细。</Typography>
      </>}
    </Stack>
    <TaskDetailDrawer key={activeTask?.id ?? 'none'} taskId={activeTask?.id ?? null} canEdit={activeTask?.can_edit} onClose={() => setTaskId(null)} onChanged={reload} />
  </PageContainer>;
}
