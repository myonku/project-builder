package attachments

import (
	"context"
	"fmt"

	"github.com/myonku/project-builder/internal/domain"
)

// All stages share this candidate scope, including soft-deleted task exclusion.
func mineScope(f MineFilter, joins string) (string, []any) {
	query := ` FROM attachments a
 LEFT JOIN tasks t ON t.id=a.task_id AND t.deleted_at IS NULL
 JOIN projects p ON p.id=COALESCE(a.project_id,t.project_id)` + joins + `
 WHERE a.uploader_id=$1 AND (a.task_id IS NULL OR t.id IS NOT NULL)`
	args := []any{f.ActorID}
	if f.DeletedOnly {
		query += ` AND a.deleted_at IS NOT NULL`
	} else if !f.IncludeDeleted {
		query += ` AND a.deleted_at IS NULL`
	}
	if f.OriginalName != "" {
		args = append(args, f.OriginalName)
		query += fmt.Sprintf(` AND strpos(lower(a.original_name),lower($%d::text))>0`, len(args))
	}
	if f.ContainerType == "project" {
		query += ` AND a.project_id IS NOT NULL`
	} else if f.ContainerType == "task" {
		query += ` AND a.task_id IS NOT NULL`
	}
	if f.ProjectID != nil {
		args = append(args, *f.ProjectID)
		query += fmt.Sprintf(` AND p.id=$%d::uuid`, len(args))
	}
	return query, args
}
func mineVisibleScope(f MineFilter, projects, tasks []string) (string, []any) {
	query, args := mineScope(f, "")
	args = append(args, projects, tasks)
	query += fmt.Sprintf(` AND (a.project_id=ANY($%d::uuid[]) OR a.task_id=ANY($%d::uuid[]))`, len(args)-1, len(args))
	return query, args
}

func (r *Repository) MineContexts(ctx context.Context, f MineFilter) ([]MineContext, error) {
	// Joins belong before WHERE; each membership has a unique (container,user) key.
	joins := ` JOIN groups g ON g.id=p.group_id
 LEFT JOIN project_members pm ON pm.project_id=p.id AND pm.user_id=$1
 LEFT JOIN group_members gm ON gm.group_id=p.group_id AND gm.user_id=$1 `
	scope, args := mineScope(f, joins)
	query := `SELECT DISTINCT p.id,p.name,p.group_id,p.status,p.visibility,p.archived_at,
 g.status,pm.role,gm.role,gm.status,t.id,t.name,COALESCE(t.is_shared,false)` + scope
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询本人附件权限范围失败: %w", err)
	}
	defer rows.Close()
	result := []MineContext{}
	for rows.Next() {
		var c MineContext
		var taskID, taskName *string
		var shared bool
		if err := rows.Scan(&c.Project.ID, &c.Project.Name, &c.Project.GroupID, &c.Project.Status, &c.Project.Visibility, &c.Project.ArchivedAt, &c.Group.Status, &c.ProjectRole, &c.GroupRole, &c.GroupMemberStatus, &taskID, &taskName, &shared); err != nil {
			return nil, err
		}
		c.Group.ID = c.Project.GroupID
		if taskID != nil {
			c.Task = &domain.Task{ID: *taskID, ProjectID: c.Project.ID, Name: *taskName, IsShared: shared}
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *Repository) CountMine(ctx context.Context, f MineFilter, projects, tasks []string) (int, error) {
	scope, args := mineVisibleScope(f, projects, tasks)
	var total int
	err := r.pool.QueryRow(ctx, `SELECT count(*)`+scope, args...).Scan(&total)
	return total, err
}
func (r *Repository) ListMine(ctx context.Context, f MineFilter, projects, tasks []string, order string, limit, offset int) ([]Record, error) {
	scope, args := mineVisibleScope(f, projects, tasks)
	args = append(args, limit, offset)
	query := `SELECT ` + attachmentColumns + scope + fmt.Sprintf(` ORDER BY %s LIMIT $%d OFFSET $%d`, order, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
