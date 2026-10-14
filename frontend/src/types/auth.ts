/**
 * 认证域请求体（对应《A01》与 F02 §6.1）。
 */

export interface LoginBody {
  /** 用户名或邮箱，不区分大小写 */
  identifier: string;
  password: string;
}

export interface RegisterBody {
  /** 3–64；`[A-Za-z0-9_-]`；保存前转小写 */
  username: string;
  /** ≤320 */
  email: string;
  /** 1–128 */
  display_name: string;
  /** ≥ security.password_min_length（默认 8）；≤256；**不做 trim** */
  password: string;
  /** IANA；省略或空串存 UTC */
  timezone?: string;
}
