/**
 * 审计记录详情抽屉（`components/audit/`）。
 *
 * `GET /audit` 没有"单条记录"端点（《A09》只有一个集合端点），列表返回的行已经包含全部字段，
 * 所以这里**不重新请求**，直接把选中的行传进来。这样也避免为了看 `changes` 再查一次接口。
 *
 * 展示口径与《A09》一致：
 * - `changes` 是扁平的"字段名 → 新值"，**没有旧值**；
 * - 敏感键已由服务端替换为 `[redacted]`，前端原样展示，不尝试还原；
 * - `actor_name` 是操作人**当前**显示名，不是操作时刻的快照；
 * - `request_id` 是当初产生这条记录的那次请求的 id，不是本次查询的 id。
 */

import * as React from 'react';
import Box from '@mui/material/Box';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import StatusTag from '@/components/common/StatusTag';
import {
  auditActionMeta,
  auditChangeEntries,
  auditObjectTypeLabel,
} from '@/components/audit/auditMeta';
import { useAuthStore } from '@/stores/auth';
import type { AuditRecord } from '@/types/audit';
import { formatForDisplay } from '@/utils/datetime';

export interface AuditDetailDrawerProps {
  /** 当前选中的记录；`null` 表示关闭 */
  record: AuditRecord | null;
  onClose: () => void;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack
      direction="row"
      spacing={2}
      sx={{ alignItems: 'flex-start', justifyContent: 'space-between' }}
    >
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box sx={{ textAlign: 'right', minWidth: 0, wordBreak: 'break-word' }}>{children}</Box>
    </Stack>
  );
}

export default function AuditDetailDrawer({ record, onClose }: AuditDetailDrawerProps) {
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const actionMeta = record ? auditActionMeta(record.action) : null;
  const changes = record ? auditChangeEntries(record.changes) : [];

  return (
    <Drawer
      anchor="right"
      open={record !== null}
      onClose={onClose}
      slotProps={{
        paper: {
          sx: {
            width: { xs: '100%', sm: 560 },
            // 顶栏是 absolute 布局的 AppBar：抽屉从视口顶部开始时会被它盖住，
            // 这里按 MUI Toolbar 的默认高度（xs 56 / sm 64）让出顶栏。
            top: { xs: 56, sm: 64 },
            height: { xs: 'calc(100% - 56px)', sm: 'calc(100% - 64px)' },
          },
        },
      }}
    >
      {record ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
          <Stack
            direction="row"
            sx={{ alignItems: 'center', justifyContent: 'space-between', px: 2, py: 1.5 }}
          >
            <Box sx={{ minWidth: 0 }}>
              <Typography variant="h6" component="h2">
                操作记录详情
              </Typography>
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}
              >
                {record.id}
              </Typography>
            </Box>
            <IconButton aria-label="关闭详情" onClick={onClose}>
              <CloseIcon />
            </IconButton>
          </Stack>
          <Divider />

          <Box sx={{ flex: 1, overflow: 'auto', p: 2 }}>
            <Stack spacing={1.5}>
              <Field label="操作时间">
                <Typography variant="body2">
                  {formatForDisplay(record.created_at, userTimeZone)}
                </Typography>
              </Field>
              <Field label="操作人">
                <Typography variant="body2">{record.actor_name || '—'}</Typography>
                <Typography
                  variant="caption"
                  color="text.secondary"
                  sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}
                >
                  {record.actor_id}
                </Typography>
              </Field>
              <Field label="动作">
                {actionMeta ? (
                  <StatusTag label={actionMeta.label} tone={actionMeta.tone} />
                ) : (
                  <Typography variant="body2">{record.action}</Typography>
                )}
              </Field>
              <Field label="对象类型">
                <StatusTag label={auditObjectTypeLabel(record.object_type)} variant="outlined" />
              </Field>
              <Field label="对象 id">
                <Typography
                  variant="caption"
                  sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}
                >
                  {record.object_id}
                </Typography>
              </Field>
              <Field label="request_id">
                <Typography
                  variant="caption"
                  sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}
                >
                  {record.request_id}
                </Typography>
              </Field>
            </Stack>

            <Divider sx={{ my: 2 }} />

            <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
              变更明细（仅新值）
            </Typography>
            <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
              服务端只记录新值、不记录旧值；敏感键的值已脱敏为 <code>[redacted]</code>，
              单个值最多保留 200 个字符。
            </Typography>
            {changes.some(([, value]) => value === '<由请求提供>') ? (
              <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 1.5 }}>
                <code>&lt;由请求提供&gt;</code> 表示该自由文本字段的正文未写入审计，
                不是实际内容（后端只留占位，避免把描述类长文本写进记录）。
              </Typography>
            ) : null}

            {changes.length > 0 ? (
              <Box
                component="dl"
                sx={{
                  display: 'grid',
                  gridTemplateColumns: 'minmax(120px, 32%) 1fr',
                  columnGap: 2,
                  rowGap: 1,
                  m: 0,
                }}
              >
                {changes.map(([key, value]) => (
                  <React.Fragment key={key}>
                    <Box
                      component="dt"
                      sx={{
                        fontFamily: 'monospace',
                        fontSize: '0.8125rem',
                        wordBreak: 'break-word',
                        color: 'text.secondary',
                      }}
                    >
                      {key}
                    </Box>
                    <Box
                      component="dd"
                      sx={{
                        m: 0,
                        fontFamily: 'monospace',
                        fontSize: '0.8125rem',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                      }}
                    >
                      {value === '' ? '（空串）' : value}
                    </Box>
                  </React.Fragment>
                ))}
              </Box>
            ) : (
              <Typography variant="body2" color="text.secondary">
                无字段变更。上传、密码类操作只记录动作与对象；提醒重试只记录状态与原因。
              </Typography>
            )}
          </Box>
        </Box>
      ) : null}
    </Drawer>
  );
}
