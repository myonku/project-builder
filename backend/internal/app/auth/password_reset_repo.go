package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

func (r *Repository) IssuePasswordReset(ctx context.Context, in ResetIssue) (ResetDelivery, error) {
	var out ResetDelivery
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		// Only email lookup, never a username-or-email OR query.
		var userID, email, name, status string
		err := tx.QueryRow(ctx, `SELECT id,email,display_name,status FROM users WHERE lower(email)=lower($1) FOR UPDATE`, in.Email).Scan(&userID, &email, &name, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != domain.UserStatusActive || email == "" {
			return nil
		}
		if _, err = tx.Exec(ctx, `INSERT INTO password_reset_states(id,user_id) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING`, in.StateID, userID); err != nil {
			return err
		}
		var last *time.Time
		if err = tx.QueryRow(ctx, `SELECT last_self_service_reset_at FROM password_reset_states WHERE user_id=$1`, userID).Scan(&last); err != nil {
			return err
		}
		if last != nil && in.Now.Before(last.Add(in.Cooldown)) {
			return nil
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND requested_at>$2`, userID, in.WindowStart).Scan(&count); err != nil {
			return err
		}
		if count >= in.MaxRequests {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE password_reset_tokens SET consumed_at=$2,updated_at=$2 WHERE user_id=$1 AND consumed_at IS NULL`, userID, in.Now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO password_reset_tokens(id,user_id,token_hash,requested_at,expires_at) VALUES($1,$2,$3,$4,$5)`, in.ID, userID, in.Hash, in.Now, in.ExpiresAt); err != nil {
			return err
		}
		out = ResetDelivery{ID: in.ID, Email: email, DisplayName: name}
		return nil
	})
	return out, err
}
func (r *Repository) RecordResetDelivery(ctx context.Context, id string, at time.Time, sent bool) error {
	if sent {
		_, err := r.pool.Exec(ctx, `UPDATE password_reset_tokens SET sent_at=$2,updated_at=$2 WHERE id=$1`, id, at)
		return err
	}
	_, err := r.pool.Exec(ctx, `UPDATE password_reset_tokens SET failed_at=$2,last_error='SMTP delivery failed',updated_at=$2 WHERE id=$1`, id, at)
	return err
}
func (r *Repository) ResetTokenUser(ctx context.Context, hash string, now time.Time) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx, `SELECT user_id FROM password_reset_tokens WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>$2`, hash, now).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrPasswordResetTokenInvalid
	}
	return userID, err
}
func (r *Repository) ConfirmPasswordReset(ctx context.Context, in ResetConfirm) error {
	return apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		var status, email string
		if err := tx.QueryRow(ctx, `SELECT status,email FROM users WHERE id=$1 FOR UPDATE`, in.UserID).Scan(&status, &email); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrPasswordResetTokenInvalid
			}
			return err
		}
		if status != domain.UserStatusActive || email == "" {
			return domain.ErrPasswordResetTokenInvalid
		}
		var last *time.Time
		if err := tx.QueryRow(ctx, `SELECT last_self_service_reset_at FROM password_reset_states WHERE user_id=$1`, in.UserID).Scan(&last); err != nil {
			return err
		}
		if last != nil && in.Now.Before(last.Add(in.Cooldown)) {
			return domain.ErrPasswordResetTokenInvalid
		}
		tag, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET consumed_at=$3,updated_at=$3 WHERE user_id=$1 AND token_hash=$2 AND consumed_at IS NULL AND expires_at>$3`, in.UserID, in.Hash, in.Now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrPasswordResetTokenInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,session_version=session_version+1,updated_at=now() WHERE id=$1`, in.UserID, in.PasswordHash); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE password_reset_states SET last_self_service_reset_at=$2,updated_at=$2 WHERE user_id=$1`, in.UserID, in.Now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE password_reset_tokens SET consumed_at=$2,updated_at=$2 WHERE user_id=$1 AND consumed_at IS NULL`, in.UserID, in.Now); err != nil {
			return err
		}
		return in.Audit.Flush(ctx, tx)
	})
}
