/**
 * 审计域接口（对应《A09》）。
 *
 * 审计记录**与业务写在同一个事务里**；`changes` 已扁平化、脱敏（`"[redacted]"`）并截断。
 * 可见性：**仅系统管理员**（含组管理员在内的其他角色一律 `403 permission_denied`）——
 * 因此"看不到"不代表"没有这条"，而是没有权限查看审计域。
 */

import { apiGetQuery } from './client';
import type { Page } from '@/types/common';
import type { AuditQuery, AuditRecord } from '@/types/audit';
import type { QueryValue } from './client';

/** `GET /audit` */
export function queryAudit(
  query: AuditQuery = {},
  signal?: AbortSignal,
): Promise<Page<AuditRecord>> {
  return apiGetQuery<AuditRecord>('/audit', { ...query } as Record<string, QueryValue>, {
    signal,
  });
}
