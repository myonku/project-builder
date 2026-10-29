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
	"github.com/myonku/project-builder/internal/platform/mail"
)

// Delivery is a single committed permission to invoke Sender once. Its token
// identifies immutable evidence; it is never a reclaimable queue lease.
type Delivery struct {
	Token   string
	Item    DispatchItem
	Message mail.Message
}

func (r *Repository) DueIDs(ctx context.Context, at time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT r.id FROM reminders r LEFT JOIN reminder_delivery_guards g
 ON (g.task_id,g.recipient_id,g.kind)=(r.task_id,r.recipient_id,r.kind)
 WHERE r.status='pending' AND r.scheduled_for<=$1 AND g.active_attempt IS NULL
 ORDER BY r.scheduled_for,r.id LIMIT $2`, at, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func lockDeliveryTask(ctx context.Context, tx apperr.Querier, rid string) (string, error) {
	var tid, pid string
	if err := tx.QueryRow(ctx, `SELECT t.id,t.project_id FROM tasks t JOIN reminders r ON r.task_id=t.id WHERE r.id=$1`, rid).Scan(&tid, &pid); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, pid); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, tid); err != nil {
		return "", err
	}
	return tid, nil
}

func (r *Repository) Authorize(ctx context.Context, rid string, at time.Time, timeout time.Duration, render func(DispatchItem) mail.Message) (*Delivery, error) {
	var delivery *Delivery
	err := apperr.InTx(clock.WithTime(ctx, at), r.pool, func(ctx context.Context, tx apperr.Querier) error {
		tid, err := lockDeliveryTask(ctx, tx, rid)
		if apperr.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT sync_task_reminders($1,$2,false)`, tid, at); err != nil {
			return err
		}
		var uid, kind string
		if err = tx.QueryRow(ctx, `SELECT recipient_id,kind FROM reminders WHERE id=$1`, rid).Scan(&uid, &kind); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO reminder_delivery_guards(task_id,recipient_id,kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tid, uid, kind); err != nil {
			return err
		}
		var active *string
		if err = tx.QueryRow(ctx, `SELECT active_attempt FROM reminder_delivery_guards WHERE task_id=$1 AND recipient_id=$2 AND kind=$3 FOR UPDATE`, tid, uid, kind).Scan(&active); err != nil {
			return err
		}
		if active != nil {
			return nil
		}
		item, err := loadDispatch(ctx, tx, rid)
		if err != nil {
			return err
		}
		if item.Status != "pending" || item.ScheduledFor.After(at) {
			return nil
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT reminder_eligible($1,$2)`, tid, uid).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return nil
		}
		var cover *string
		if err = tx.QueryRow(ctx, `SELECT reminder_success($1,$2,$3,$4,$5)`, tid, uid, kind, item.Recipient.Timezone, at).Scan(&cover); err != nil {
			return err
		}
		if cover != nil {
			_, err = tx.Exec(ctx, `UPDATE reminders SET status='suppressed',covered_by=$2,last_error='同日已发送',updated_at=$3 WHERE id=$1`, rid, *cover, at)
			return err
		}
		message := render(item)
		snapshot, err := json.Marshal(message)
		if err != nil {
			return err
		}
		var token string
		err = tx.QueryRow(ctx, `INSERT INTO reminder_delivery_attempts(reminder_id,task_id,recipient_id,kind,plan_version,cycle,event_id,
   authorized_at,expires_at,status,timezone,snapshot) SELECT r.id,r.task_id,r.recipient_id,r.kind,r.plan_version,r.cycle,r.event_id,$2,$3,'sending',$4,$5::jsonb||jsonb_build_object('task_version',t.updated_at,'project_version',p.updated_at) FROM reminders r JOIN tasks t ON t.id=r.task_id JOIN projects p ON p.id=t.project_id WHERE r.id=$1 RETURNING id`,
			rid, at, at.Add(timeout), item.Recipient.Timezone, snapshot).Scan(&token)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE reminder_delivery_guards SET active_attempt=$4 WHERE task_id=$1 AND recipient_id=$2 AND kind=$3`, tid, uid, kind, token); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE reminders SET status='sending',recipient_email=$2,attempts=attempts+1,last_error='',updated_at=$3 WHERE id=$1`, rid, message.To, at); err != nil {
			return err
		}
		delivery = &Delivery{Token: token, Item: item, Message: message}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return delivery, nil
}

func loadDispatch(ctx context.Context, tx apperr.Querier, id string) (DispatchItem, error) {
	var item DispatchItem
	err := tx.QueryRow(ctx, `SELECT `+reminderColumns+`,t.id,t.name,p.id,p.name,t.status,t.progress,
 COALESCE((SELECT string_agg(COALESCE(NULLIF(x.display_name,''),x.username),'、' ORDER BY x.username)
 FROM task_members m JOIN users x ON x.id=m.user_id WHERE m.task_id=t.id AND m.role='owner'),''),t.start_date,t.end_date,
 u.id,u.email,u.status,u.reminder_enabled,reminder_timezone(u.timezone)
 FROM reminders r JOIN tasks t ON t.id=r.task_id JOIN projects p ON p.id=t.project_id JOIN users u ON u.id=r.recipient_id WHERE r.id=$1`, id).Scan(
		&item.ID, &item.TaskID, &item.RecipientID, &item.RecipientEmail, &item.Kind, &item.ScheduledFor, &item.Status, &item.DedupeKey, &item.Attempts, &item.LastError, &item.SentAt, &item.CreatedAt, &item.UpdatedAt, &item.Cycle, &item.PlanVersion, &item.CoveredBy, &item.EventID, &item.Legacy, &item.CanRetry,
		&item.Candidate.TaskID, &item.Candidate.TaskName, &item.Candidate.ProjectID, &item.Candidate.ProjectName, &item.Candidate.Status, &item.Candidate.Progress, &item.Candidate.OwnerNames, &item.Candidate.StartDate, &item.Candidate.EndDate,
		&item.Recipient.UserID, &item.Recipient.Email, &item.Recipient.Status, &item.Recipient.ReminderEnabled, &item.Recipient.Timezone)
	// The envelope address is captured at authorization, never at plan creation.
	item.RecipientEmail = item.Recipient.Email
	return item, err
}

func (r *Repository) Settle(ctx context.Context, d *Delivery, status string, at time.Time, reason string) error {
	return apperr.InTx(clock.WithTime(ctx, at), r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if _, err := lockDeliveryTask(ctx, tx, d.Item.ID); err != nil {
			return err
		}
		var active *string
		if err := tx.QueryRow(ctx, `SELECT active_attempt FROM reminder_delivery_guards WHERE task_id=$1 AND recipient_id=$2 AND kind=$3 FOR UPDATE`, d.Item.TaskID, d.Item.RecipientID, d.Item.Kind).Scan(&active); err != nil {
			return err
		}
		if active == nil || *active != d.Token {
			return domain.ErrConflict.WithField("attempt", "发送令牌已失效")
		}
		// A late result may settle its own unknown attempt, but never an administratively resolved one.
		tag, err := tx.Exec(ctx, `UPDATE reminder_delivery_attempts SET status=$2::text,sent_at=CASE WHEN $2::text='sent' THEN $3::timestamptz ELSE NULL END,error=$4
   WHERE id=$1 AND status IN ('sending','unknown') AND resolution IS NULL`, d.Token, status, at, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE reminders SET status=$2::text,sent_at=CASE WHEN $2::text='sent' THEN $3::timestamptz ELSE NULL END,last_error=$4,updated_at=$3 WHERE id=$1 AND status IN ('sending','unknown')`, d.Item.ID, status, at, reason); err != nil {
			return err
		}
		if status != "unknown" {
			if _, err = tx.Exec(ctx, `UPDATE reminder_delivery_guards SET active_attempt=NULL WHERE active_attempt=$1`, d.Token); err != nil {
				return err
			}
		}
		if status == "sent" {
			// New events and changed node cycles that became due while SMTP was in flight
			// are consumed by today's success, never retried tomorrow by an unrelated edit.
			_, err = tx.Exec(ctx, `UPDATE reminders SET status='suppressed',covered_by=$4,last_error='同日已发送',updated_at=$5
    WHERE task_id=$1 AND recipient_id=$2 AND kind=$3 AND status='pending' AND scheduled_for<=$5`, d.Item.TaskID, d.Item.RecipientID, d.Item.Kind, d.Token, at)
		}
		return err
	})
}

func (r *Repository) Expire(ctx context.Context, at time.Time) error {
	// One statement locks attempts in a deterministic order. It never takes project
	// locks after guard locks; the active token remains owned and cannot be reused.
	_, err := r.pool.Exec(ctx, `WITH expired AS (
  UPDATE reminder_delivery_attempts SET status='unknown',error='发送授权超时，须核实；禁止自动重发'
  WHERE status='sending' AND expires_at<=$1 RETURNING reminder_id)
  UPDATE reminders SET status='unknown',last_error='发送授权超时，须核实；禁止自动重发',updated_at=$1
  WHERE id IN(SELECT reminder_id FROM expired) AND status='sending'`, at)
	return err
}

// Initialize is an explicit, resumable maintenance operation, not a scheduler job.
// Each project is a checkpoint. Dry-run rolls back all of its writes.
func (r *Repository) Initialize(ctx context.Context, at time.Time, allowOverdue, dryRun bool) (int, error) {
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, maintenanceLock).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, fmt.Errorf("派发尚未停止")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(c, `SELECT pg_advisory_unlock($1)`, maintenanceLock); err != nil {
			_ = conn.Close(c)
		}
	}()

	rows, err := r.pool.Query(ctx, `SELECT id FROM projects ORDER BY id`)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return total, err
		}
		_, err = tx.Exec(ctx, `SELECT sync_project_reminders($1,$2,true)`, id, at)
		if err == nil && !allowOverdue {
			_, err = tx.Exec(ctx, `UPDATE reminders r SET status='cancelled',last_error='首次初始化逾期积压未放行'
   FROM tasks t WHERE t.id=r.task_id AND t.project_id=$1 AND r.kind='task_overdue' AND r.status='pending' AND r.scheduled_for<=$2`, id, at)
		}
		var n int
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT count(*) FROM reminders r JOIN tasks t ON t.id=r.task_id WHERE t.project_id=$1 AND r.status='pending'`, id).Scan(&n)
		}
		if err != nil || dryRun {
			_ = tx.Rollback(ctx)
		} else {
			err = tx.Commit(ctx)
		}
		if err != nil {
			return total, fmt.Errorf("initialize project %s: %w", id, err)
		}
		total += n
	}
	return total, nil
}
