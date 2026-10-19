/**
 * 甘特图项目下拉的选项来源（`POST /projects/query`，《A04》）。
 *
 * 与 `useGroupOptions` 同构：一次最多取 100 个（《S02》§2.5 的上限），
 * **不把选项列表当权限依据**——能不能看某项目的甘特图仍由
 * `GET /plan-views/projects/{id}/gantt` 判定。
 *
 * 这里刻意不接 `useListQuery`：下拉只需要一份轻量选项，不需要分页/排序状态机。
 */

import * as React from 'react';
import { queryProjects } from '@/api/projects';
import { toApiError, type ApiError } from '@/api/client';
import type { ProjectStatus } from '@/types/project';

const PAGE_SIZE = 100;

export interface ProjectOption {
  id: string;
  name: string;
  status: ProjectStatus;
}

export interface UseProjectOptionsResult {
  options: ProjectOption[];
  loading: boolean;
  error: ApiError | null;
  reload: () => void;
}

/**
 * @param enabled 为 false 时不发请求（成员视图用不到项目下拉）
 * @param name 已应用的项目名称筛选；空串表示不筛。**不要传输入框草稿值**，
 *             否则每敲一个字都会打一次 `POST /projects/query`。
 */
export function useProjectOptions(
  enabled = true,
  name = '',
): UseProjectOptionsResult {
  const [options, setOptions] = React.useState<ProjectOption[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [reloadToken, setReloadToken] = React.useState(0);

  React.useEffect(() => {
    if (!enabled) {
      return undefined;
    }
    const controller = new AbortController();
    let cancelled = false;

    setLoading(true);
    setError(null);

    void (async () => {
      try {
        const appliedName = name.trim();
        const page = await queryProjects(
          {
            page: 1,
            page_size: PAGE_SIZE,
            sort: 'name',
            ...(appliedName ? { name: appliedName } : {}),
          },
          controller.signal,
        );
        if (cancelled) {
          return;
        }
        setOptions(
          (page.data ?? []).map((project) => ({
            id: project.id,
            name: project.name,
            status: project.status,
          })),
        );
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [enabled, name, reloadToken]);

  const reload = React.useCallback(() => setReloadToken((token) => token + 1), []);

  return { options, loading, error, reload };
}
