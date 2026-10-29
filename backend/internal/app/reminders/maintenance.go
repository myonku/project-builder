package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
)

const maintenanceLock int64 = 80021102

// The shared operational lock spans SMTP, but holds no transaction/business
// rows. An exclusive maintenance lock prevents overlap with a live dispatch.
func (r *Repository) BeginDispatch(ctx context.Context) (func(), bool, error) {
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, false, err
	}
	var ok bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, maintenanceLock).Scan(&ok); err != nil || !ok {
		_ = conn.Close(context.Background())
		return nil, false, err
	}
	return func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(c, `SELECT pg_advisory_unlock_shared($1)`, maintenanceLock); err != nil {
			_ = conn.Close(c)
		}
		_ = conn.Close(context.Background())
	}, true, nil
}

// ResolveUnknown requires an administrator's evidence and a stopped-worker
// attestation. Lease expiry alone never proves an old process cannot send.
func (r *Repository) ResolveUnknown(ctx context.Context, token, actor, outcome, evidence string, sentAt *time.Time, stopped bool, at time.Time) error {
	if !stopped || evidence == "" {
		return fmt.Errorf("须确认全部派发工作进程已终止，并提供核实证据")
	}
	if outcome != "accepted" && outcome != "not_accepted" {
		return fmt.Errorf("无法确认的结果须保持隔离")
	}
	if outcome == "accepted" && (sentAt == nil || sentAt.After(at)) {
		return fmt.Errorf("确认已接受须提供不晚于当前时间的实际接受时刻")
	}
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var ok bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, maintenanceLock).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("仍有派发运行，拒绝核实处置")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(c, `SELECT pg_advisory_unlock($1)`, maintenanceLock); err != nil {
			_ = conn.Close(c)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT set_config('worklane.reminder_now',$1,true)`, at.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND system_role='admin')`, actor).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return domain.ErrConflict.WithField("actor", "仅启用的系统管理员可核实")
	}
	var rid, tid, uid, kind string
	if err = tx.QueryRow(ctx, `SELECT reminder_id,task_id,recipient_id,kind FROM reminder_delivery_attempts WHERE id=$1`, token).Scan(&rid, &tid, &uid, &kind); err != nil {
		return err
	}
	if _, err = lockDeliveryTask(ctx, tx, rid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT task_id FROM reminder_delivery_guards WHERE task_id=$1 AND recipient_id=$2 AND kind=$3 FOR UPDATE`, tid, uid, kind); err != nil {
		return err
	}
	resolution, _ := json.Marshal(map[string]any{"actor_id": actor, "outcome": outcome, "evidence": evidence, "resolved_at": at, "dispatchers_stopped": true})
	status := "failed"
	if outcome == "accepted" {
		status = "sent"
	}
	tag, err := tx.Exec(ctx, `UPDATE reminder_delivery_attempts SET status=$2::text,sent_at=$3,resolution=$4
 WHERE id=$1 AND status='unknown' AND resolution IS NULL`, token, status, sentAt, resolution)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict.WithField("attempt", "尝试已被结算或核实")
	}
	planStatus := status
	if outcome == "not_accepted" && (kind == "task_assigned" || kind == "task_owner_changed") {
		planStatus = "cancelled"
	}
	if _, err = tx.Exec(ctx, `UPDATE reminders SET status=$2::text,sent_at=$3,last_error='管理员已核实：'||$4::text,updated_at=$5 WHERE id=$1 AND status='unknown'`, rid, planStatus, sentAt, evidence, at); err != nil {
		return err
	}
	// Legacy data may have multiple uncertain attempts for the same scope.
	if _, err = tx.Exec(ctx, `UPDATE reminder_delivery_guards g SET active_attempt=(SELECT a.id FROM reminder_delivery_attempts a
 WHERE (a.task_id,a.recipient_id,a.kind)=(g.task_id,g.recipient_id,g.kind) AND a.status='unknown' ORDER BY a.authorized_at,a.id LIMIT 1)
 WHERE task_id=$1 AND recipient_id=$2 AND kind=$3`, tid, uid, kind); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT sync_task_reminders($1,$2,false)`, tid, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReconcileTask is the explicit transactional repair entry for maintenance.
func (r *Repository) ReconcileTask(ctx context.Context, tid string, at time.Time) error {
	return apperr.InTx(clock.WithTime(ctx, at), r.pool, func(ctx context.Context, tx apperr.Querier) error {
		_, err := tx.Exec(ctx, `SELECT sync_task_reminders($1,$2,false)`, tid, at)
		return err
	})
}
