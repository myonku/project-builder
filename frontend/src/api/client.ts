/**
 * HTTP 客户端：整个前端**唯一**直接使用 `fetch` 的地方。
 *
 * 承担四件事（《F01》§5.2）：
 * 1. Cookie：每请求 `credentials: 'include'`——会话是 HttpOnly Cookie，前端读不到也不能读；
 * 2. 信封解包：成功响应统一解包 `{ data, pagination }`；`204` 返回 `undefined`；
 * 3. 错误封装：非 2xx 一律抛 `ApiError`，携带 status / code / message / fields / requestId / retryAfterSeconds；
 * 4. 限流：`429` 时读取 `Retry-After` 写入 `retryAfterSeconds`；**不做自动重试**。
 *
 * **本文件不加工任何业务字段**（尤其 `updated_at` 必须原样字符串返回，见 F01 §5.1）。
 */

import type { Envelope, ErrorBody, Page } from '@/types/common';

const RAW_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1';

/** API 前缀，例如 `/api/v1`；开发期由 Vite 代理转发到后端 */
export const API_BASE = RAW_BASE.replace(/\/+$/, '');

export type HttpMethod = 'GET' | 'POST' | 'PATCH' | 'DELETE';

export type QueryValue = string | number | boolean | null | undefined;

export interface ApiErrorInit {
  status: number;
  code: string;
  message: string;
  fields?: Record<string, string>;
  requestId?: string | null;
  retryAfterSeconds?: number | null;
}

/**
 * 统一的接口错误。**错误码只做消费，不做定义**（F01 §3.2）：
 * 文案与分支都基于《S02》§2.4 的错误码表，前端不新增错误码。
 */
export class ApiError extends Error {
  /** HTTP 状态码；`0` 表示请求没能到达服务端（网络/代理错误） */
  readonly status: number;
  /** 机器可读错误码，如 `version_conflict` */
  readonly code: string;
  /** 字段级明细（422 与部分 409/403） */
  readonly fields: Record<string, string>;
  /** 与响应头 `X-Request-ID` 一致，便于反馈问题 */
  readonly requestId: string | null;
  /** 429 的 `Retry-After`（秒） */
  readonly retryAfterSeconds: number | null;

  constructor(init: ApiErrorInit) {
    super(init.message);
    this.name = 'ApiError';
    this.status = init.status;
    this.code = init.code;
    this.fields = init.fields ?? {};
    this.requestId = init.requestId ?? null;
    this.retryAfterSeconds = init.retryAfterSeconds ?? null;
  }

  get isNetworkError(): boolean {
    return this.status === 0;
  }

  /** 未登录 / 会话失效 */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }

  /** 已登录但无权限（**不要**当成"未登录"） */
  get isForbidden(): boolean {
    return this.status === 403;
  }

  /** `*_not_found` 同时代表"不可见"（《S02》§5 加强项） */
  get isNotFound(): boolean {
    return this.status === 404;
  }

  get isConflict(): boolean {
    return this.status === 409;
  }

  get isValidationError(): boolean {
    return this.status === 422;
  }

  get isRateLimited(): boolean {
    return this.status === 429;
  }

  /** 版本冲突：交给 `useVersionedForm` 让用户选"放弃/保留" */
  get isVersionConflict(): boolean {
    return this.code === 'version_conflict';
  }
}

export interface RequestConfig {
  method: HttpMethod;
  /** 以 `/` 开头的路径，如 `/projects/query`（不含 API 前缀） */
  path: string;
  body?: unknown;
  query?: Record<string, QueryValue>;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export function buildQueryString(query?: Record<string, QueryValue>): string {
  if (!query) {
    return '';
  }
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    // 空串按"未提供"处理：计划视图等端点对未知/非法参数一律 400
    if (value === undefined || value === null || value === '') {
      continue;
    }
    params.append(key, String(value));
  }
  const serialized = params.toString();
  return serialized ? `?${serialized}` : '';
}

function urlOf(path: string, query?: Record<string, QueryValue>): string {
  return `${API_BASE}${path}${buildQueryString(query)}`;
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError';
}

function parseErrorBody(payload: unknown): ErrorBody | null {
  if (typeof payload !== 'object' || payload === null || !('error' in payload)) {
    return null;
  }
  const { error } = payload as { error?: unknown };
  if (typeof error !== 'object' || error === null) {
    return null;
  }
  const body = error as Partial<ErrorBody>;
  if (typeof body.code !== 'string' || typeof body.message !== 'string') {
    return null;
  }
  return {
    code: body.code,
    message: body.message,
    fields: body.fields ?? {},
    request_id: typeof body.request_id === 'string' ? body.request_id : '',
  };
}

function retryAfterOf(response: Response): number | null {
  const raw = response.headers.get('Retry-After');
  if (!raw) {
    return null;
  }
  const seconds = Number.parseInt(raw, 10);
  return Number.isFinite(seconds) ? seconds : null;
}

const STATUS_FALLBACK_MESSAGE: Record<number, string> = {
  400: '请求参数不合法。',
  401: '登录状态已失效，请重新登录。',
  403: '你没有执行该操作的权限。',
  404: '对象不存在或你没有访问权限。',
  409: '数据已发生变化，请刷新后重试。',
  413: '文件超过了大小限制。',
  415: '不支持该文件类型。',
  422: '提交的内容未通过校验。',
  429: '请求过于频繁，请稍后再试。',
  500: '服务暂时不可用，请稍后重试。',
};

function errorFromResponse(response: Response, payload: unknown): ApiError {
  const body = parseErrorBody(payload);
  const status = response.status;
  return new ApiError({
    status,
    code: body?.code ?? `http_${status}`,
    message: body?.message || STATUS_FALLBACK_MESSAGE[status] || '请求失败。',
    fields: body?.fields,
    requestId: body?.request_id || response.headers.get('X-Request-ID'),
    retryAfterSeconds: retryAfterOf(response),
  });
}

async function readPayload(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

async function send(
  config: RequestConfig & { rawBody?: BodyInit },
): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...config.headers,
  };
  let body: BodyInit | undefined = config.rawBody;
  if (config.rawBody === undefined && config.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(config.body);
  }
  try {
    return await fetch(urlOf(config.path, config.query), {
      method: config.method,
      headers,
      body,
      // 会话是 HttpOnly Cookie：必须每请求带上（《S02》§2.2）
      credentials: 'include',
      signal: config.signal,
    });
  } catch (error) {
    if (isAbortError(error)) {
      throw error;
    }
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: '暂时无法连接，请检查网络后重试。',
    });
  }
}

/** 发起请求并返回原始信封（需要 `pagination` 时用这个） */
export async function request<T>(config: RequestConfig): Promise<Envelope<T>> {
  const response = await send(config);
  if (response.status === 204) {
    return { data: undefined as T };
  }
  const payload = await readPayload(response);
  if (!response.ok) {
    throw errorFromResponse(response, payload);
  }
  if (
    typeof payload !== 'object' ||
    payload === null ||
    !('data' in payload)
  ) {
    throw new ApiError({
      status: response.status,
      code: 'invalid_response',
      message: '暂时无法读取信息，请稍后重试。',
      requestId: response.headers.get('X-Request-ID'),
    });
  }
  return payload as Envelope<T>;
}

export async function requestData<T>(config: RequestConfig): Promise<T> {
  const envelope = await request<T>(config);
  return envelope.data;
}

export function apiGet<T>(
  path: string,
  options?: { query?: Record<string, QueryValue>; signal?: AbortSignal },
): Promise<T> {
  return requestData<T>({ method: 'GET', path, ...options });
}

export function apiPost<T>(
  path: string,
  body?: unknown,
  options?: { signal?: AbortSignal; headers?: Record<string, string> },
): Promise<T> {
  return requestData<T>({ method: 'POST', path, body, ...options });
}

export function apiPatch<T>(
  path: string,
  body: unknown,
  options?: { signal?: AbortSignal },
): Promise<T> {
  return requestData<T>({ method: 'PATCH', path, body, ...options });
}

export function apiDelete<T>(
  path: string,
  body?: unknown,
  options?: { signal?: AbortSignal },
): Promise<T> {
  return requestData<T>({ method: 'DELETE', path, body, ...options });
}

/** `POST .../query`：分页一定存在（F02 §7） */
export async function apiPostQuery<T>(
  path: string,
  body?: unknown,
  options?: { signal?: AbortSignal },
): Promise<Page<T>> {
  const envelope = await request<T[]>({
    method: 'POST',
    path,
    body,
    ...options,
  });
  return {
    data: envelope.data ?? [],
    pagination: envelope.pagination ?? {
      page: 1,
      page_size: 0,
      total: 0,
      has_next: false,
    },
  };
}

/** `GET` 上的列表端点（reminders / audit）：分页来自信封 */
export async function apiGetQuery<T>(
  path: string,
  query?: Record<string, QueryValue>,
  options?: { signal?: AbortSignal },
): Promise<Page<T>> {
  const envelope = await request<T[]>({
    method: 'GET',
    path,
    query,
    ...options,
  });
  return {
    data: envelope.data ?? [],
    pagination: envelope.pagination ?? {
      page: 1,
      page_size: 0,
      total: 0,
      has_next: false,
    },
  };
}

/**
 * 上传：`multipart/form-data`，字段名固定为 `file`，单文件（《A07》）。
 * 调用方需要把 `413 / 415 / 409（配额）` 映射为可读提示。
 */
export async function apiUpload<T>(
  path: string,
  file: File,
  options?: { field?: string; signal?: AbortSignal },
): Promise<T> {
  const form = new FormData();
  form.append(options?.field ?? 'file', file);
  const response = await send({
    method: 'POST',
    path,
    rawBody: form,
    signal: options?.signal,
  });
  const payload = await readPayload(response);
  if (!response.ok) {
    throw errorFromResponse(response, payload);
  }
  if (typeof payload !== 'object' || payload === null || !('data' in payload)) {
    throw new ApiError({
      status: response.status,
      code: 'invalid_response',
      message: '暂时无法读取信息，请稍后重试。',
    });
  }
  return (payload as Envelope<T>).data;
}

export interface DownloadedFile {
  blob: Blob;
  /** 来自 `Content-Disposition`；缺失时为 null，由调用方兜底命名 */
  filename: string | null;
}

function filenameOf(response: Response): string | null {
  const disposition = response.headers.get('Content-Disposition');
  if (!disposition) {
    return null;
  }
  const utf8 = /filename\*=UTF-8''([^;]+)/i.exec(disposition);
  if (utf8) {
    try {
      return decodeURIComponent(utf8[1]);
    } catch {
      return utf8[1];
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(disposition);
  return plain ? plain[1] : null;
}

/**
 * 下载：`GET /attachments/{id}/download` **不是 JSON 信封**，响应体是字节流。
 * 用 `fetch` 取 blob 而不是 `<a href>`——错误会以 JSON 返回，
 * 直接跳转会让浏览器存下一个坏文件（F01 §5.6）。
 */
export async function apiDownload(
  path: string,
  options?: { signal?: AbortSignal },
): Promise<DownloadedFile> {
  const response = await send({ method: 'GET', path, signal: options?.signal });
  if (!response.ok) {
    throw errorFromResponse(response, await readPayload(response));
  }
  const blob = await response.blob();
  return { blob, filename: filenameOf(response) };
}

/** 把任意异常收敛成 `ApiError`，供 hook 与 UI 统一消费 */
export function toApiError(error: unknown): ApiError {
  if (error instanceof ApiError) {
    return error;
  }
  if (isAbortError(error)) {
    return new ApiError({ status: 0, code: 'aborted', message: '请求已取消。' });
  }
  return new ApiError({
    status: 0,
    code: 'unknown_error',
    message: error instanceof Error ? error.message : '发生未知错误。',
  });
}
