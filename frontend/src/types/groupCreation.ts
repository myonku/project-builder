import type { PaginationQuery } from './common';
import type { JoinRequestStatus } from './group';

export interface GroupCreationRequest {
  id: string;
  name: string;
  description: string | null;
  reason: string;
  status: JoinRequestStatus;
  created_at: string;
  updated_at: string;
  reviewed_at: string | null;
  rejection_reason: string | null;
  group_id: string | null;
  group_name: string | null;
  group_status: 'active' | 'disabled' | null;
  can_access_group: boolean;
  applicant_id?: string;
  username?: string;
  display_name?: string;
  applicant_status?: 'active' | 'disabled';
  reviewed_by?: string | null;
}
export interface CreateGroupApplication {
  name: string;
  description?: string;
  reason: string;
}
export interface GroupCreationQuery extends PaginationQuery {
  status?: JoinRequestStatus;
  q?: string;
  applicant_id?: string;
  sort?: string;
}
