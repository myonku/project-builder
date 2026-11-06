package groups

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// Lock all active admins in id order before the applicant, matching users.ensureAdminRetained.
// No existing project is locked here: approval only inserts a new, empty group.
// SHARE (not KEY SHARE) prevents status/role changes until the authorization commits.
func lockCreationUsers(ctx context.Context, tx apperr.Querier, actorID, applicantID string, admin, activeApplicant bool) error {
	if admin {
		rows, err := tx.Query(ctx, `SELECT id FROM users WHERE system_role='admin' AND status='active' ORDER BY id FOR SHARE`)
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if id == actorID {
				found = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrPermissionDenied
		}
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, applicantID).Scan(&status); err != nil {
		if apperr.IsNoRows(err) {
			return domain.ErrResourceNotFound
		}
		return err
	}
	if activeApplicant && status != domain.UserStatusActive {
		return domain.ErrConflict.WithField("applicant_id", "申请人账号已停用")
	}
	return nil
}

const creationFrom = ` FROM group_creation_requests r JOIN users u ON u.id=r.applicant_id
 LEFT JOIN groups g ON g.id=r.group_id AND (g.owner_id=$1 OR EXISTS(SELECT 1 FROM users a WHERE a.id=$1 AND a.system_role='admin' AND a.status='active')
 OR EXISTS(SELECT 1 FROM group_members m WHERE m.group_id=g.id AND m.user_id=$1 AND m.status='active')) `
const creationColumns = `r.id,r.applicant_id,r.name,r.description,r.reason,r.status,r.reviewed_by,r.reviewed_at,r.rejection_reason,r.group_id,
 r.created_at,r.updated_at,u.username,u.display_name,u.status,g.name,g.status,(g.id IS NOT NULL)`

func scanCreation(row pgx.Row) (CreationRequestRecord, error) {
	var r CreationRequestRecord
	err := row.Scan(&r.ID, &r.ApplicantID, &r.Name, &r.Description, &r.Reason, &r.Status, &r.ReviewedBy, &r.ReviewedAt, &r.RejectionReason, &r.GroupID,
		&r.CreatedAt, &r.UpdatedAt, &r.Username, &r.DisplayName, &r.ApplicantStatus, &r.GroupName, &r.GroupStatus, &r.CanAccessGroup)
	if apperr.IsNoRows(err) {
		return r, domain.ErrResourceNotFound
	}
	return r, err
}
func findCreation(ctx context.Context, q apperr.Querier, id, actorID string) (CreationRequestRecord, error) {
	return scanCreation(q.QueryRow(ctx, `SELECT `+creationColumns+creationFrom+` WHERE r.id=$2`, actorID, id))
}
func (r *Repository) FindCreationRequest(ctx context.Context, id, actorID string) (CreationRequestRecord, error) {
	return findCreation(ctx, r.pool, id, actorID)
}

func (r *Repository) SubmitCreationRequest(ctx context.Context, p SubmitCreationParams) (CreationRequestRecord, bool, error) {
	var result CreationRequestRecord
	replay := false
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := lockCreationUsers(ctx, tx, p.ActorID, p.ActorID, false, true); err != nil {
			return err
		}
		// Serialize only the same user/scope/key; the UNIQUE constraint remains authoritative.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.ActorID+":"+creationScope+":"+p.Key); err != nil {
			return err
		}
		var resultID string
		err := tx.QueryRow(ctx, `SELECT result_id FROM idempotency_keys WHERE user_id=$1 AND scope=$2 AND idempotency_key=$3`, p.ActorID, creationScope, p.Key).Scan(&resultID)
		if err == nil {
			replay = true
			result, err = findCreation(ctx, tx, resultID, p.ActorID)
			return err
		}
		if !apperr.IsNoRows(err) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO group_creation_requests(id,applicant_id,name,description,reason) VALUES($1,$2,$3,$4,$5)`, p.ID, p.ActorID, p.Name, p.Description, p.Reason); err != nil {
			return apperr.Translate(err, creationConstraints)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(id,user_id,scope,idempotency_key,result_id) VALUES($1,$2,$3,$4,$5)`, p.KeyID, p.ActorID, creationScope, p.Key, p.ID); err != nil {
			return apperr.TranslateWrite(err)
		}
		if err := p.Audit.Flush(ctx, tx); err != nil {
			return err
		}
		result, err = findCreation(ctx, tx, p.ID, p.ActorID)
		return err
	})
	return result, replay, err
}

func (r *Repository) QueryCreationRequests(ctx context.Context, f CreationFilter, order string, limit, offset int) ([]CreationRequestRecord, int, error) {
	const where = ` WHERE ($2::uuid IS NULL OR r.applicant_id=$2) AND ($3::text IS NULL OR r.status=$3)
 AND ($4='' OR strpos(lower(r.name),lower($4))>0 OR strpos(lower(u.username),lower($4))>0 OR strpos(lower(u.display_name),lower($4))>0)`
	args := []any{f.ActorID, f.ApplicantID, f.Status, f.Q}
	// A single read-only snapshot keeps total and page consistent under concurrent reviews.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*)`+creationFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := tx.Query(ctx, `SELECT `+creationColumns+creationFrom+where+` ORDER BY `+order+` LIMIT $5 OFFSET $6`, args...)
	if err != nil {
		return nil, 0, err
	}
	items := make([]CreationRequestRecord, 0)
	for rows.Next() {
		rec, err := scanCreation(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		items = append(items, rec)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *Repository) ReviewCreationRequest(ctx context.Context, p ReviewCreationParams) (CreationRequestRecord, error) {
	var result CreationRequestRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := lockCreationUsers(ctx, tx, p.ActorID, p.ApplicantID, true, p.Status == "approved"); err != nil {
			return err
		}
		var status string
		var versionIsCurrent bool
		err := tx.QueryRow(ctx, `SELECT status,updated_at=$2 FROM group_creation_requests WHERE id=$1 FOR UPDATE`, p.ID, p.ExpectedUpdated).Scan(&status, &versionIsCurrent)
		if apperr.IsNoRows(err) {
			return domain.ErrResourceNotFound
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return domain.ErrConflict.WithField("status", "申请已审核，请核对最新结果")
		}
		if !versionIsCurrent {
			return domain.ErrVersionConflict
		}
		rec, err := findCreation(ctx, tx, p.ID, p.ActorID)
		if err != nil {
			return err
		}
		var groupID *string
		if p.Status == "approved" {
			if _, err := insertGroupInTx(ctx, tx, CreateGroupParams{ID: p.GroupID, Name: rec.Name, Description: rec.Description, OwnerID: rec.ApplicantID, MemberID: p.MemberID, ActorID: p.ActorID, Audit: p.GroupAudit}); err != nil {
				return err
			}
			groupID = &p.GroupID
		}
		tag, err := tx.Exec(ctx, `UPDATE group_creation_requests SET status=$2,reviewed_by=$3,reviewed_at=now(),rejection_reason=$4,group_id=$5,updated_at=now()
   WHERE id=$1 AND status='pending' AND updated_at=$6`, p.ID, p.Status, p.ActorID, p.RejectionReason, groupID, p.ExpectedUpdated)
		if err != nil {
			return apperr.TranslateWrite(err)
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrVersionConflict
		}
		if err := p.Audit.Flush(ctx, tx); err != nil {
			return err
		}
		result, err = findCreation(ctx, tx, p.ID, p.ActorID)
		return err
	})
	if err != nil {
		return CreationRequestRecord{}, fmt.Errorf("审核工作组创建申请: %w", err)
	}
	return result, nil
}

var creationConstraints = map[string]apperr.ConstraintError{
	"uq_group_creation_requests_pending_name": {Code: domain.CodeConflict, Message: "已存在同名待审申请", Field: "name", Detail: "请先查看同名新建申请的审核结果"},
}
