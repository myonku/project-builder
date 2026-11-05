import * as React from 'react';
import { Button, Stack, ToggleButton, ToggleButtonGroup } from '@mui/material';
import { Link as RouterLink } from 'react-router';
import PageContainer from '@/components/common/PageContainer';
import InsightOverview from '@/components/statistics/InsightOverview';
import { useAuthStore } from '@/stores/auth';
export default function InsightDashboardView() {
 const timezone = useAuthStore(state => state.user?.timezone) ?? 'UTC';
 const [scope, setScope] = React.useState<'self' | 'participating'>('self');
 return <PageContainer title="概览" actions={<Button component={RouterLink} to="/statistics?scope=self&page=report">生成本周汇报</Button>}><Stack spacing={2}>
  <Stack direction="row" sx={{gap: 1, flexWrap: 'wrap', justifyContent: 'space-between'}}><ToggleButtonGroup value={scope} exclusive size="small" onChange={(_, value) => value && setScope(value)} aria-label="概览任务范围"><ToggleButton value="self">本人承担</ToggleButton><ToggleButton value="participating">参与项目</ToggleButton></ToggleButtonGroup><Button component={RouterLink} to="/statistics?scope=self">查看数据概览</Button></Stack>
  <InsightOverview key={scope} query={{scope, timezone}} compact /><Button component={RouterLink} to="/me" sx={{alignSelf: 'flex-start'}}>个人资料与账号信息</Button>
 </Stack></PageContainer>;
}
