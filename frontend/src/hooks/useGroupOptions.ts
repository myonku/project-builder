/**
 * 工作组下拉选项（项目列表的"工作组"筛选、以及后续项目表单都要用）。
 *
 * 数据源是 `POST /groups/query`（《A03》）。两个已知边界：
 * - `page_size` 上限 100（《S02》§2.5），因此这里**最多**拿 100 个工作组；
 *   内部小组数量远小于这个量级，超过时再改成"搜索式下拉 + 分页"；
 * - 普通成员只会看到自己所在的工作组，系统管理员会看到全部——
 *   因此选项列表**不能**当作权限判断依据，能不能建项目由后端在 `POST /projects` 上判定。
 */

import * as React from 'react';
import { queryGroups } from '@/api/groups';
import { toApiError, type ApiError } from '@/api/client';
import type { GroupMemberRole, GroupMembershipStatus } from '@/types/group';

export interface GroupOption {
  id: string;
  name: string;
  /** 派生字段：当前用户在该组的角色，仅用于展示/排序 */
  membershipRole: GroupMemberRole | null;
  membershipStatus: GroupMembershipStatus | null;
}

export interface UseGroupOptionsResult {
  options: GroupOption[];
  /** id → 名称，列表里显示工作组名用 */
  nameById: Map<string, string>;
  loading: boolean;
  error: ApiError | null;
  reload: () => void;
}

const PAGE_SIZE = 100;

export function useGroupOptions(enabled = true): UseGroupOptionsResult {
  const [options, setOptions] = React.useState<GroupOption[]>([]);
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
        const page = await queryGroups(
          { page: 1, page_size: PAGE_SIZE, sort: 'name' },
          controller.signal,
        );
        if (cancelled) {
          return;
        }
        setOptions(
          (page.data ?? []).map((group) => ({
            id: group.id,
            name: group.name,
            membershipRole: group.membership_role,
            membershipStatus: group.membership_status,
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
  }, [enabled, reloadToken]);

  const nameById = React.useMemo(
    () => new Map(options.map((option) => [option.id, option.name])),
    [options],
  );

  const reload = React.useCallback(() => setReloadToken((token) => token + 1), []);

  return { options, nameById, loading, error, reload };
}
