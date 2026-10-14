/**
 * 接口错误 → UI 的映射（F01 §5.2）。
 *
 * 映射表（与文档逐条对应）：
 *
 * | 状态码 | 处理 |
 * | --- | --- |
 * | `401` | 清 `authStore` → 跳 `/login?redirect=<原路径>` |
 * | `403` | 提示"权限不足"；**不要**当成"未登录" |
 * | `404` | 统一文案"对象不存在或你没有访问权限"（`*_not_found` 双语义，《S02》§5） |
 * | `409` | 默认不提示，交给调用方分流（`version_conflict` / `duplicate_member` / `conflict`） |
 * | `422` | 由 `applyApiFieldErrors` 回填到 React Hook Form |
 * | `429` | 提示"请求过于频繁，请 N 秒后再试" |
 * | `5xx` | 提示服务端异常，并附上 `request_id` |
 */

import * as React from 'react';
import { useNavigate } from 'react-router';
import type { FieldValues, Path, UseFormSetError } from 'react-hook-form';
import { toApiError, type ApiError } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import { useNotifications } from '@/hooks/useNotifications';

/** 把错误翻成一句可直接展示的中文 */
export function describeApiError(error: unknown): string {
  const apiError = toApiError(error);
  if (apiError.isNetworkError) {
    return apiError.message;
  }
  switch (apiError.status) {
    case 401:
      return '登录状态已失效，请重新登录。';
    case 403:
      return '权限不足，无法执行该操作。';
    case 404:
      // `*_not_found` 同时代表"不存在"与"不可见"：不要渲染成"404 页面"
      return '对象不存在或你没有访问权限。';
    case 409:
      return apiError.isVersionConflict
        ? '数据已被其他人修改，请刷新后重试。'
        : '当前状态不允许该操作（可能已归档或存在重复数据）。';
    case 413:
      return '文件超过大小限制。';
    case 415:
      return '不支持该文件类型。';
    case 422:
      return '提交的内容未通过校验，请检查表单。';
    case 429: {
      const seconds = apiError.retryAfterSeconds;
      return seconds
        ? `请求过于频繁，请 ${seconds} 秒后再试。`
        : '请求过于频繁，请稍后再试。';
    }
    default:
      if (apiError.status >= 500) {
        return apiError.requestId
          ? `服务端异常，请稍后重试（request_id: ${apiError.requestId}）`
          : '服务端异常，请稍后重试。';
      }
      return apiError.message;
  }
}

/**
 * 把 `422` 的 `fields` 回填到 React Hook Form 的字段级错误。
 *
 * @returns 是否至少命中了一个表单字段（false 表示用户应看全局提示）
 */
export function applyApiFieldErrors<T extends FieldValues>(
  error: unknown,
  setError: UseFormSetError<T>,
): boolean {
  const apiError = toApiError(error);
  const entries = Object.entries(apiError.fields);
  if (entries.length === 0) {
    return false;
  }
  let applied = false;
  for (const [field, message] of entries) {
    setError(field as Path<T>, { type: 'server', message });
    applied = true;
  }
  return applied;
}

export interface HandleApiErrorOptions {
  /** 覆盖默认文案 */
  message?: string;
  /** true 时只做 401 跳转等副作用，不弹提示（调用方自己展示） */
  silent?: boolean;
  /** 401 时是否跳转登录页，默认 true */
  redirectOnUnauthorized?: boolean;
}

export interface UseApiErrorResult {
  /** 执行副作用（401 跳转、Snackbar 提示），并返回归一化后的 ApiError 供调用方分流 */
  handle: (error: unknown, options?: HandleApiErrorOptions) => ApiError;
  describe: typeof describeApiError;
  /** 422 字段回填的便捷入口 */
  applyFieldErrors: typeof applyApiFieldErrors;
}

export function useApiError(): UseApiErrorResult {
  const { show } = useNotifications();
  const navigate = useNavigate();
  const clear = useAuthStore((state) => state.clear);

  const handle = React.useCallback(
    (error: unknown, options?: HandleApiErrorOptions): ApiError => {
      const apiError = toApiError(error);

      if (apiError.isUnauthorized) {
        clear();
        if (options?.redirectOnUnauthorized !== false) {
          const redirect = `${window.location.pathname}${window.location.search}`;
          navigate(`/login?redirect=${encodeURIComponent(redirect)}`, {
            replace: true,
          });
        }
        if (!options?.silent) {
          show('登录状态已失效，请重新登录。', { severity: 'warning' });
        }
        return apiError;
      }

      if (!options?.silent && apiError.status !== 409 && apiError.status !== 422) {
        show(options?.message ?? describeApiError(apiError), {
          severity: apiError.status === 403 ? 'warning' : 'error',
        });
      }

      return apiError;
    },
    [clear, navigate, show],
  );

  return React.useMemo(
    () => ({ handle, describe: describeApiError, applyFieldErrors: applyApiFieldErrors }),
    [handle],
  );
}
