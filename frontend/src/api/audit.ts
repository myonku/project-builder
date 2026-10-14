/**
 * 审计域接口（对应《A09》）。
 *
 * 审计记录**与业务写在同一个事务里**；`changes` 已扁平化、脱敏（`"[redacted]"`）并截断。
 * 可见性：系统管理员可查全部，普通成员只能看到自己可见对象上的记录——
 * 因此"看不到某条"不代表"没有这条"。
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
