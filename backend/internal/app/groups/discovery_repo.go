package groups

import (
	"context"
	"fmt"
)

const discoverFrom = ` FROM groups g
 LEFT JOIN group_members m ON m.group_id=g.id AND m.user_id=$1
 LEFT JOIN group_join_requests r ON r.group_id=g.id AND r.user_id=$1
 WHERE g.status='active' AND ($2='' OR strpos(lower(g.name),lower($2))>0 OR g.id::text=lower($2))`

// strpos gives literal substring matching, including %, _ and backslashes.
func (r *Repository) CountDiscoverGroups(ctx context.Context, actorID, q string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*)`+discoverFrom, actorID, q).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计可申请工作组: %w", err)
	}
	return n, nil
}
func (r *Repository) ListDiscoverGroups(ctx context.Context, actorID, q, order string, limit, offset int) ([]DiscoverGroup, error) {
	rows, err := r.pool.Query(ctx, `SELECT g.id,g.name,g.description,g.status,m.status,r.status,
 COALESCE(g.owner_id=$1 OR (m.status='active' AND m.role='manager') OR EXISTS(SELECT 1 FROM users u WHERE u.id=$1 AND u.system_role='admin'),false)`+discoverFrom+` ORDER BY `+order+` LIMIT $3 OFFSET $4`, actorID, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询可申请工作组: %w", err)
	}
	defer rows.Close()
	items := make([]DiscoverGroup, 0)
	for rows.Next() {
		var item DiscoverGroup
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.MembershipStatus, &item.RequestStatus, &item.CanReview); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const myJoinFrom = ` FROM group_join_requests r JOIN groups g ON g.id=r.group_id
 LEFT JOIN group_members m ON m.group_id=g.id AND m.user_id=$1
 WHERE r.user_id=$1 AND ($2::text IS NULL OR r.status=$2)`

func (r *Repository) CountMyJoinRequests(ctx context.Context, actorID string, status *string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*)`+myJoinFrom, actorID, status).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计本人工作组申请: %w", err)
	}
	return n, nil
}
func (r *Repository) ListMyJoinRequests(ctx context.Context, actorID string, status *string, order string, limit, offset int) ([]MyJoinRequest, error) {
	rows, err := r.pool.Query(ctx, `SELECT r.id,g.id,g.name,g.status,m.status,r.status,r.reviewed_at,r.created_at,r.updated_at`+myJoinFrom+` ORDER BY `+order+` LIMIT $3 OFFSET $4`, actorID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询本人工作组申请: %w", err)
	}
	defer rows.Close()
	items := make([]MyJoinRequest, 0)
	for rows.Next() {
		var item MyJoinRequest
		if err := rows.Scan(&item.ID, &item.GroupID, &item.GroupName, &item.GroupStatus, &item.MembershipStatus, &item.Status, &item.ReviewedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
