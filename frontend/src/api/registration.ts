/**
 * 注册申请审核接口（对应《A10》）。
 *
 * `POST /auth/register` 仍留在 api/auth.ts；本文件只放管理员审核端点。
 */

import { apiPost, apiPostQuery } from './client';
import type { Page } from '@/types/common';
import type {
  RegistrationRequest,
  RegistrationRequestQuery,
  RegistrationReviewBody,
  RegistrationReviewResult,
} from '@/types/registration';

/** `POST /registration-requests/query`：仅系统管理员 */
export function queryRegistrationRequests(
  body: RegistrationRequestQuery,
  signal?: AbortSignal,
): Promise<Page<RegistrationRequest>> {
  return apiPostQuery<RegistrationRequest>('/registration-requests/query', body, { signal });
}

/** `POST /registration-requests/{id}/review`：仅系统管理员 */
export function reviewRegistrationRequest(
  requestId: string,
  body: RegistrationReviewBody,
): Promise<RegistrationReviewResult> {
  return apiPost<RegistrationReviewResult>(
    `/registration-requests/${requestId}/review`,
    body,
  );
}
