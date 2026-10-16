/**
 * 计划视图的数据加载（个人计划与组内计划共用，F05 §7.2 第 4 步）。
 *
 * 与列表页的 `useListQuery` **刻意不同**：三个计划视图端点都**不分页**（《A06》），
 * 所以这里没有分页/排序状态机，只有"参数 → 一次 GET → payload"。
 *
 * 两件容易踩的事在这里集中处理：
 * 1. **项目下拉的选项来源**：筛选一旦带上 `project_id`，响应里的 `projects` 就只剩一个，
 *    拿它当下拉选项会让用户"选完就没了别的选项"。因此这里把**未带 `project_id` 那次**的
 *    `projects` 留作选项（`projectOptions`），与当前响应解耦。
 * 2. **竞态**：快速改筛选会连发多个请求，用 `AbortController` + 序号双保险，
 *    只认最后一次的结果（和 `useListQuery` 同款做法）。
 */

import * as React from 'react';
import { getGroupPlan, getPersonalPlan } from '@/api/planViews';
import { toApiError, type ApiError } from '@/api/client';
import type { PlanProject, PlanViewPayload, PlanViewQuery } from '@/types/plan';

export interface UsePlanViewOptions {
  /** `null` = 个人计划；给值 = 组内计划（该组的共享计划） */
  groupId: string | null;
  /** 已应用的查询参数（对象身份变化即触发重新加载） */
  query: PlanViewQuery;
  /** 组内计划在"还没选工作组"时传 false，避免发出没有 id 的请求 */
  enabled?: boolean;
}

export interface UsePlanViewResult {
  payload: PlanViewPayload | null;
  loading: boolean;
  error: ApiError | null;
  /** 项目下拉选项：来自**未按项目筛选**的那次响应 */
  projectOptions: PlanProject[];
  reload: () => void;
}

export function usePlanView({
  groupId,
  query,
  enabled = true,
}: UsePlanViewOptions): UsePlanViewResult {
  const [payload, setPayload] = React.useState<PlanViewPayload | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [projectOptions, setProjectOptions] = React.useState<PlanProject[]>([]);
  const [reloadToken, setReloadToken] = React.useState(0);
  const requestSeq = React.useRef(0);

  React.useEffect(() => {
    if (!enabled) {
      setPayload(null);
      setError(null);
      return undefined;
    }
    const controller = new AbortController();
    const seq = requestSeq.current + 1;
    requestSeq.current = seq;

    setLoading(true);
    setError(null);

    void (async () => {
      try {
        const next =
          groupId === null
            ? await getPersonalPlan(query, controller.signal)
            : await getGroupPlan(groupId, query, controller.signal);
        if (requestSeq.current !== seq) {
          return; // 已有更新的请求发出，丢弃本次结果
        }
        setPayload(next);
        if (!query.project_id) {
          // 只有"未按项目筛选"的响应才能给出完整的项目选项
          setProjectOptions(next.projects);
        }
      } catch (caught) {
        if (requestSeq.current !== seq) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setPayload(null);
        }
      } finally {
        if (requestSeq.current === seq) {
          setLoading(false);
        }
      }
    })();

    return () => controller.abort();
  }, [enabled, groupId, query, reloadToken]);

  const reload = React.useCallback(() => setReloadToken((token) => token + 1), []);

  return { payload, loading, error, projectOptions, reload };
}
