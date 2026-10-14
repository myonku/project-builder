/**
 * 幂等键（《S02》§2.6.1，只有 `POST /tasks/{id}/copy` 用到）。
 *
 * 用法（F01 §5.5）：
 * - 一次"用户点击复制"生成一个 key；
 * - 网络抖动导致的重试**复用同一个 key**（首次 201、重放 200）；
 * - 换一个任务复制时**换 key**（调 `renew()`）。
 *
 * 后端幂等键作用域是 `tasks.copy:<task_id>` 且包含用户，
 * 因此同一个 key 用在别的任务上不会互相影响——但前端仍按任务换 key，
 * 保证"同一 key 只对应一次用户意图"。
 */

import * as React from 'react';

/** 生成一个幂等键；`crypto.randomUUID` 在安全上下文（localhost / https）可用 */
export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}

export interface UseIdempotencyKeyResult {
  /** 当前 key */
  key: string;
  /** 换一个新的 key（切换被复制对象时调用），返回新值便于立即使用 */
  renew: () => string;
}

export function useIdempotencyKey(initialKey?: string): UseIdempotencyKeyResult {
  const [key, setKey] = React.useState(() => initialKey ?? newIdempotencyKey());

  const renew = React.useCallback(() => {
    const next = newIdempotencyKey();
    setKey(next);
    return next;
  }, []);

  return { key, renew };
}
