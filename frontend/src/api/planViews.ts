/**
 * 计划视图接口（对应《A06》）。
 *
 * 三个端点都**没有分页**；个人计划与组内计划共用同一套查询参数，
 * **未知参数一律 400**（因此空的筛选值必须在 `buildQueryString` 里被丢掉）。
 * 项目甘特图端点**不接受任何参数**（携带即 400）。
 */

import { apiGet } from './client';
import type { GanttPayload, PlanViewPayload, PlanViewQuery } from '@/types/plan';
import type { QueryValue } from './client';

function toQuery(query: PlanViewQuery): Record<string, QueryValue> {
  return { ...query };
}

/** `GET /plan-views/personal` */
export function getPersonalPlan(
  query: PlanViewQuery = {},
  signal?: AbortSignal,
): Promise<PlanViewPayload> {
  return apiGet<PlanViewPayload>('/plan-views/personal', {
    query: toQuery(query),
    signal,
  });
}

/** `GET /plan-views/groups/{id}` */
export function getGroupPlan(
  groupId: string,
  query: PlanViewQuery = {},
  signal?: AbortSignal,
): Promise<PlanViewPayload> {
  return apiGet<PlanViewPayload>(`/plan-views/groups/${groupId}`, {
    query: toQuery(query),
    signal,
  });
}

/** `GET /plan-views/projects/{id}/gantt`：不带任何查询参数 */
export function getProjectGantt(
  projectId: string,
  signal?: AbortSignal,
): Promise<GanttPayload> {
  return apiGet<GanttPayload>(`/plan-views/projects/${projectId}/gantt`, { signal });
}
