/**
 * 计划视图的汇总卡片：直接展示 `data.summary`（《A06》§1）。
 *
 * 五个数字与表格内容**由同一个数据集算出**（服务端口径），因此这里不做任何再计算——
 * 前端一旦自己算，就会出现"卡片说 3 个逾期、表格里有 4 行标红"这类自相矛盾。
 */

import * as React from 'react';
import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Grid from '@mui/material/Grid';
import LinearProgress from '@mui/material/LinearProgress';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import type { PlanSummary } from '@/types/plan';

function Tile({
  label,
  value,
  tone = 'default',
  hint,
}: {
  label: string;
  value: React.ReactNode;
  tone?: 'default' | 'primary' | 'error';
  hint?: string;
}) {
  return (
    <Card variant="outlined" sx={{ height: '100%' }}>
      <CardContent sx={{ py: 1.5, '&:last-child': { pb: 1.5 } }}>
        <Typography variant="caption" color="text.secondary">
          {label}
        </Typography>
        <Typography
          variant="h5"
          sx={{
            fontVariantNumeric: 'tabular-nums',
            color: tone === 'error' ? 'error.main' : tone === 'primary' ? 'primary.main' : 'text.primary',
          }}
        >
          {value}
        </Typography>
        {hint ? (
          <Typography variant="caption" color="text.secondary">
            {hint}
          </Typography>
        ) : null}
      </CardContent>
    </Card>
  );
}

export interface PlanSummaryTilesProps {
  summary: PlanSummary | null | undefined;
}

export default function PlanSummaryTiles({ summary }: PlanSummaryTilesProps) {
  const overall = Math.min(100, Math.max(0, Math.round(summary?.overall_progress ?? 0)));

  return (
    <Grid container spacing={2}>
      <Grid size={{ xs: 6, sm: 4, md: 2 }}>
        <Tile label="项目" value={summary?.project_count ?? 0} />
      </Grid>
      <Grid size={{ xs: 6, sm: 4, md: 2 }}>
        <Tile label="任务" value={summary?.task_count ?? 0} />
      </Grid>
      <Grid size={{ xs: 6, sm: 4, md: 2 }}>
        <Tile label="进行中" value={summary?.in_progress_task_count ?? 0} tone="primary" />
      </Grid>
      <Grid size={{ xs: 6, sm: 4, md: 2 }}>
        <Tile
          label="逾期"
          value={summary?.overdue_task_count ?? 0}
          tone={(summary?.overdue_task_count ?? 0) > 0 ? 'error' : 'default'}
          hint={(summary?.overdue_task_count ?? 0) > 0 ? '需要关注' : undefined}
        />
      </Grid>
      <Grid size={{ xs: 12, sm: 8, md: 4 }}>
        <Card variant="outlined" sx={{ height: '100%' }}>
          <CardContent sx={{ py: 1.5, '&:last-child': { pb: 1.5 } }}>
            <Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'baseline' }}>
              <Typography variant="caption" color="text.secondary">
                总体进度
              </Typography>
              <Typography variant="h6" sx={{ fontVariantNumeric: 'tabular-nums' }}>
                {overall}%
              </Typography>
            </Stack>
            <Box sx={{ mt: 1 }}>
              <LinearProgress variant="determinate" value={overall} sx={{ height: 8, borderRadius: 4 }} />
            </Box>
            <Typography variant="caption" color="text.secondary">
              任务平均进度，不含已取消任务
            </Typography>
          </CardContent>
        </Card>
      </Grid>
    </Grid>
  );
}
