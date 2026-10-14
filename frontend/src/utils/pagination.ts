/**
 * 分页相关的常量与换算。
 *
 * MUI `DataGrid` 的 `paginationModel.page` **从 0 开始**，后端 `page` **从 1 开始**：
 * 换算**只在这里做一次**，不要散落到组件里（F02 §2.1）。
 */

/** 后端默认值（《S02》§2.5）：不显式设置会让"打开列表立刻请求 page_size=100" */
export const DEFAULT_PAGE_SIZE = 20;

/** 后端上限（《S02》§2.5） */
export const MAX_PAGE_SIZE = 100;

export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100] as const;

/** DataGrid 的 0 基页码 → 后端的 1 基页码 */
export function toServerPage(gridPage: number): number {
  return gridPage + 1;
}

/** 后端的 1 基页码 → DataGrid 的 0 基页码 */
export function toGridPage(serverPage: number): number {
  return Math.max(0, serverPage - 1);
}
