/**
 * `POST .../query` 的统一状态机（F01 §5.4）。
 *
 * 收敛四件容易做错的事：
 * 1. **分页换算**：MUI `DataGrid` 的 `page` 从 0 开始，后端从 1 开始（只在这里换算）；
 * 2. **默认 `page_size`**：显式用后端默认值 20，避免"打开列表就请求 page_size=100"；
 * 3. **排序表达式**：`[{field, sort}]` → `"field"` / `"-field"`，并**按白名单硬校验**
 *    （未知字段后端返回 400，前端先挡掉）；
 * 4. **筛选变化重置到第 1 页**。
 *
 * **筛选不走 `DataGrid` 的 `filterModel`**：后端没有对应的表达式协议，
 * 筛选由各页面自己维护表单状态，通过 `filters` / `setFilters` 传进来（F02 §2.1 第 4 条）。
 */

import * as React from 'react';
import type { GridPaginationModel, GridSortModel } from '@mui/x-data-grid';
import { toApiError, type ApiError } from '@/api/client';
import type { Page } from '@/types/common';
import { DEFAULT_PAGE_SIZE, toGridPage, toServerPage } from '@/utils/pagination';

export interface ListQueryParams<TFilters> {
  filters: TFilters;
  /** 后端页码，从 1 开始 */
  page: number;
  pageSize: number;
  /** 排序表达式；没有排序时为 undefined */
  sort: string | undefined;
}

export interface UseListQueryOptions<T, TFilters extends object> {
  /** 查询函数；内部用 ref 固定，不需要 `useCallback` 包装 */
  query: (params: ListQueryParams<TFilters>) => Promise<Page<T>>;
  initialFilters?: TFilters;
  /** 允许排序的字段白名单；未列出的排序会被丢弃 */
  sortWhitelist?: readonly string[];
  initialSort?: GridSortModel;
  initialPageSize?: number;
  /** false 时挂起（例如 id 还没拿到） */
  enabled?: boolean;
  /** 额外依赖：值变化时重新查询（例如 `projectId`） */
  deps?: readonly unknown[];
}

export interface UseListQueryResult<T, TFilters extends object> {
  rows: T[];
  /** 全量条数，来自 `pagination.total`——**不要**用 `rows.length` */
  rowCount: number;
  loading: boolean;
  error: ApiError | null;
  paginationModel: GridPaginationModel;
  onPaginationModelChange: (model: GridPaginationModel) => void;
  sortModel: GridSortModel;
  onSortModelChange: (model: GridSortModel) => void;
  filters: TFilters;
  /** 整体替换筛选；会重置到第 1 页 */
  setFilters: (next: TFilters) => void;
  /** 局部更新筛选；会重置到第 1 页 */
  patchFilters: (patch: Partial<TFilters>) => void;
  /** 保持当前筛选与页码重新查询 */
  reload: () => void;
}

/** `[{field, sort}]` → `"field"` / `"-field"`，逗号分隔 */
export function toSortExpression(
  model: GridSortModel,
  whitelist?: readonly string[],
): string | undefined {
  const parts = model
    .filter((item) => item.sort && (!whitelist || whitelist.includes(item.field)))
    .map((item) => (item.sort === 'desc' ? `-${item.field}` : item.field));
  return parts.length > 0 ? parts.join(',') : undefined;
}

/** 排序表达式 → `GridSortModel`（反向换算，供 URL 恢复等场景使用） */
export function fromSortExpression(expression: string | undefined): GridSortModel {
  if (!expression) {
    return [];
  }
  return expression
    .split(',')
    .map((part) => part.trim())
    .filter((part) => part.length > 0)
    .map((part) =>
      part.startsWith('-')
        ? { field: part.slice(1), sort: 'desc' as const }
        : { field: part, sort: 'asc' as const },
    );
}

export function useListQuery<T, TFilters extends object>(
  options: UseListQueryOptions<T, TFilters>,
): UseListQueryResult<T, TFilters> {
  const {
    query,
    initialFilters,
    sortWhitelist,
    initialSort = [],
    initialPageSize = DEFAULT_PAGE_SIZE,
    enabled = true,
    deps = [],
  } = options;

  const [rows, setRows] = React.useState<T[]>([]);
  const [rowCount, setRowCount] = React.useState(0);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [filters, setFiltersState] = React.useState<TFilters>(
    () => initialFilters ?? ({} as TFilters),
  );
  const [paginationModel, setPaginationModel] = React.useState<GridPaginationModel>(() => ({
    page: 0,
    pageSize: initialPageSize,
  }));
  const [sortModel, setSortModel] = React.useState<GridSortModel>(initialSort);
  const [reloadToken, setReloadToken] = React.useState(0);

  const queryRef = React.useRef(query);
  queryRef.current = query;

  const sortExpression = toSortExpression(sortModel, sortWhitelist);
  const filtersKey = JSON.stringify(filters);
  const depsKey = JSON.stringify(deps);

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
        const page = await queryRef.current({
          filters,
          page: toServerPage(paginationModel.page),
          pageSize: paginationModel.pageSize,
          sort: sortExpression,
        });
        if (cancelled) {
          return;
        }
        setRows(page.data ?? []);
        setRowCount(page.pagination?.total ?? 0);
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setRows([]);
          setRowCount(0);
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
    // `filtersKey` / `depsKey` 是上面若干对象的序列化形式：以它们的值作为依赖，
    // 这样调用方每次渲染新建的对象/数组不会造成无限重查。
  }, [
    enabled,
    paginationModel.page,
    paginationModel.pageSize,
    sortExpression,
    filtersKey,
    depsKey,
    reloadToken,
  ]);

  const onPaginationModelChange = React.useCallback((model: GridPaginationModel) => {
    setPaginationModel((previous) =>
      previous.page === model.page && previous.pageSize === model.pageSize
        ? previous
        : model,
    );
  }, []);

  const onSortModelChange = React.useCallback((model: GridSortModel) => {
    setSortModel(model);
    // 排序变化同样回到第 1 页，避免停在越界页
    setPaginationModel((previous) => ({ ...previous, page: 0 }));
  }, []);

  const setFilters = React.useCallback((next: TFilters) => {
    setFiltersState(next);
    setPaginationModel((previous) => ({ ...previous, page: 0 }));
  }, []);

  const patchFilters = React.useCallback((patch: Partial<TFilters>) => {
    setFiltersState((previous) => ({ ...previous, ...patch }));
    setPaginationModel((previous) => ({ ...previous, page: 0 }));
  }, []);

  const reload = React.useCallback(() => {
    setReloadToken((token) => token + 1);
  }, []);

  return {
    rows,
    rowCount,
    loading,
    error,
    paginationModel,
    onPaginationModelChange,
    sortModel,
    onSortModelChange,
    filters,
    setFilters,
    patchFilters,
    reload,
  };
}

export { toGridPage, toServerPage };
