/**
 * 浏览器本地存储的**安全读写**。
 *
 * 为什么需要包装：`localStorage` 在隐私模式 / 存储被禁用时会**抛异常**（不是返回 null），
 * 直接调用会让整个页面白屏。这里统一吞掉异常并把"存不了"当作"没有存"。
 *
 * 用途仅限**非敏感的界面偏好**（例如登录页勾选"记住登录信息"后保存的用户名）。
 * **禁止**在这里放令牌、密码或任何会话信息——会话在 HttpOnly Cookie 里（《S02》§2.2）。
 */

const PREFIX = 'pb.';

function keyOf(key: string): string {
  return `${PREFIX}${key}`;
}

export function readStored(key: string): string | null {
  try {
    return window.localStorage.getItem(keyOf(key));
  } catch {
    return null;
  }
}

export function writeStored(key: string, value: string): void {
  try {
    window.localStorage.setItem(keyOf(key), value);
  } catch {
    // 存不进去不影响功能，静默忽略
  }
}

export function removeStored(key: string): void {
  try {
    window.localStorage.removeItem(keyOf(key));
  } catch {
    // 同上
  }
}

/** 登录页"记住登录信息"保存的用户名（**不是**凭据，密码永远不落本地存储） */
export const STORAGE_KEY_LOGIN_IDENTIFIER = 'login.identifier';
