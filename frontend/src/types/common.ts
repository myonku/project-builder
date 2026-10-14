/**
 * 通用信封与分页类型（对应《S02》§2.3 / §2.4 与 F02 §2）。
 *
 * 约定：
 * - `data` 是列表时后端保证空列表为 `[]` 而不是 `null`，因此前端一律写 `T[]`；
 * - `pagination` 只出现在列表端点；`total` / `has_next` 直接来自服务端，不在客户端重算。
 */

export interface Pagination {
  page: number;
  page_size: number;
  total: number;
  has_next: boolean;
}

/** 成功信封：单对象与写操作没有 `pagination` */
export interface Envelope<T> {
  data: T;
  pagination?: Pagination;
}

/** 列表端点的返回值：`pagination` 一定存在 */
export interface Page<T> {
  data: T[];
  pagination: Pagination;
}

export interface ErrorBody {
  /** 机器可读错误码，客户端据此分支（《S02》§2.4） */
  code: string;
  /** 面向人的英文描述；中文文案由前端自己组织 */
  message: string;
  /** 字段级明细，仅校验类错误存在 */
  fields?: Record<string, string>;
  /** 与响应头 `X-Request-ID` 一致 */
  request_id: string;
}

export interface ErrorEnvelope {
  error: ErrorBody;
}

/** 各域 `*QueryBody` 共用的分页与排序字段 */
export interface PaginationQuery {
  /** ≥1，默认 1 */
  page?: number;
  /** 1–100，默认 20 */
  page_size?: number;
  /** `field` 升序 / `-field` 降序，逗号分隔多字段；白名单由各域服务端校验 */
  sort?: string;
}
