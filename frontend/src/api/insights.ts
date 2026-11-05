import { apiGet } from './client';
export interface InsightMember { user_id: string; display_name: string; username: string }
export interface InsightTask {
 id: string; project_id: string; group_id: string; name: string; project_name: string; status: string;
 progress: number; start_date: string; end_date: string; has_children: boolean; deleted: boolean;
 is_shared: boolean; visibility: string; archived: boolean; is_milestone: boolean; priority: number;
 members: InsightMember[]; can_edit: boolean;
}
export interface InsightScope { scope: 'self' | 'group' | 'member' | 'project' | 'participating'; group_id?: string; member_id?: string; project_id?: string; timezone: string; include_archived?: boolean }
export interface InsightQuery extends InsightScope { from?: string; to?: string; bucket?: 'day' | 'week'; as_of?: string }
export interface Coverage { coverage_start: string; complete: boolean; gaps: string[] }
export interface PeriodMetrics { created: number; completed: number; reopened: number; open: number; overdue: number }
export interface InsightPoint { date: string; until?: string; created: number; completed: number; open: number | null }
export interface InsightEvent { id: number; occurred_at: string; actor_id: string | null; actor_name: string; before: InsightTask | null; after: InsightTask; kind: string }
export interface Overview { member_options: InsightMember[]; project_options: Record<string, string>; filter: InsightQuery; as_of: string; coverage: Coverage; tasks: InsightTask[]; status: Record<string, number>; open: number; overdue: number; due_soon: number; due: InsightPoint[]; milestones: InsightTask[]; period: PeriodMetrics }
export interface Breakdown { id: string; name: string; completed: number; open: number }
export interface InsightReport {
 filter: InsightQuery; as_of: string; observed_until: string; coverage: Coverage; metrics: PeriodMetrics;
 series: InsightPoint[]; members: Breakdown[]; projects: Breakdown[]; achievements: InsightTask[];
 attention: InsightTask[]; next: InsightTask[]; events: InsightEvent[]; previous: PeriodMetrics | null; policy: string;
}
export const getOverview = (query: InsightQuery, signal?: AbortSignal) => apiGet<Overview>('/insights/overview', { query: { ...query }, signal });
export const getReport = (query: InsightQuery, signal?: AbortSignal) => apiGet<InsightReport>('/insights/reports', { query: { ...query }, signal });
export const getActivity = (query: InsightQuery, cursor = '', signal?: AbortSignal) => apiGet<{items: InsightEvent[]; next_cursor: string; as_of: string; coverage: Coverage}>('/insights/activity', { query: { ...query, cursor, limit: 6 }, signal });
export const getReportItems = (query: InsightQuery, metric: string, cursor = '', signal?: AbortSignal) => apiGet<{items: InsightTask[]; total: number; next_cursor: string; as_of: string}>('/insights/report-items', { query: { ...query, metric, cursor, limit: 10 }, signal });
