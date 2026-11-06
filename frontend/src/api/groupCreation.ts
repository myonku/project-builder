import { apiGet, apiPost, apiPostQuery } from './client';
import type { CreateGroupApplication, GroupCreationQuery, GroupCreationRequest } from '@/types/groupCreation';

export const submitGroupCreation = (body: CreateGroupApplication, key: string) =>
  apiPost<GroupCreationRequest>('/group-creation-requests', body, { headers: { 'Idempotency-Key': key } });
export const queryGroupCreations = (body: GroupCreationQuery, admin = false) =>
  apiPostQuery<GroupCreationRequest>(admin ? '/group-creation-requests/query' : '/me/group-creation-requests/query', body);
export const getGroupCreation = (id: string) => apiGet<GroupCreationRequest>(`/group-creation-requests/${id}`);
export const reviewGroupCreation = (id: string, body: { status: 'approved' | 'rejected'; expected_updated_at: string; rejection_reason?: string }) =>
  apiPost<GroupCreationRequest>(`/group-creation-requests/${id}/review`, body);
