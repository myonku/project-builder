/**
 * 项目甘特图加载（`GET /plan-views/projects/{id}/gantt`，《A06》§3）。
 *
 * 和 `usePlanView` 一样是"参数 → 一次 GET → payload"：端点不分页、不接受查询参数，
 * 所以这里没有筛选状态机。`projectId` 为 `null` 时不发请求。
 */

import * as React from 'react';
import { getProjectGantt } from '@/api/planViews';
import { toApiError, type ApiError } from '@/api/client';
import type { GanttPayload } from '@/types/plan';
import { useReloadCompletion } from './useReloadCompletion';

export interface UseProjectGanttResult {
  payload: GanttPayload | null;
  loading: boolean;
  error: ApiError | null;
  reload: () => void;
  reloadAsync: () => Promise<boolean>;
}

export function useProjectGantt(projectId: string | null): UseProjectGanttResult {
  const [payload, setPayload] = React.useState<GanttPayload | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const { token: reloadToken, reload, reloadAsync, settle } = useReloadCompletion();
  const requestSeq = React.useRef(0);

  React.useEffect(() => {
    if (!projectId) {
      setPayload(null);
      setError(null);
      setLoading(false);
      settle(reloadToken, false);
      return undefined;
    }

    const controller = new AbortController();
    const seq = requestSeq.current + 1;
    requestSeq.current = seq;
    setLoading(true);
    setError(null);

    void (async () => {
      try {
        const next = await getProjectGantt(projectId, controller.signal);
        if (controller.signal.aborted || requestSeq.current !== seq) {
          return;
        }
        setPayload(next);
        settle(reloadToken, true);
      } catch (caught) {
        if (requestSeq.current !== seq) {
          return;
        }
        const apiError = toApiError(caught);
        settle(reloadToken, false);
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

    return () => { controller.abort(); settle(reloadToken, false); };
  }, [projectId, reloadToken, settle]);

  return { payload, loading, error, reload, reloadAsync };
}
