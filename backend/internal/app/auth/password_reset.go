package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	mailer "github.com/myonku/project-builder/internal/platform/mail"
)

// PasswordResetStore serializes reset operations on the users row before tokens.
// Raw tokens never cross this persistence boundary.
type PasswordResetStore interface {
	IssuePasswordReset(context.Context, ResetIssue) (ResetDelivery, error)
	RecordResetDelivery(context.Context, string, time.Time, bool) error
	ResetTokenUser(context.Context, string, time.Time) (string, error)
	ConfirmPasswordReset(context.Context, ResetConfirm) error
}
type ResetIssue struct {
	ID, StateID, Email, Hash    string
	Now, ExpiresAt, WindowStart time.Time
	Cooldown                    time.Duration
	MaxRequests                 int
}
type ResetDelivery struct{ ID, Email, DisplayName string }
type ResetConfirm struct {
	UserID, Hash, PasswordHash string
	Now                        time.Time
	Cooldown                   time.Duration
	Audit                      *apperr.AuditWrite
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return domain.ErrValidation.WithField("email", "请输入有效邮箱")
	}
	if !s.security.PasswordResetEnabled || !s.mailConfig.Enabled || s.sender == nil || s.resets == nil {
		return domain.ErrPasswordResetUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return domain.ErrInternal.WithCause(err)
	}
	token := hex.EncodeToString(raw)
	id, err := s.ids.UUID()
	if err != nil {
		return domain.ErrInternal.WithCause(err)
	}
	stateID, err := s.ids.UUID()
	if err != nil {
		return domain.ErrInternal.WithCause(err)
	}
	now := s.clk.Now()
	delivery, err := s.resets.IssuePasswordReset(ctx, ResetIssue{
		ID: id, StateID: stateID, Email: email, Hash: resetHash(token), Now: now,
		ExpiresAt:   now.Add(time.Duration(s.security.PasswordResetTokenTTLSeconds) * time.Second),
		WindowStart: now.Add(-time.Duration(s.security.PasswordResetWindowSeconds) * time.Second),
		Cooldown:    time.Duration(s.security.PasswordResetCooldownSeconds) * time.Second,
		MaxRequests: s.security.PasswordResetMaxRequests,
	})
	if err != nil {
		return err
	}
	if delivery.ID == "" {
		return nil
	}
	link := strings.ReplaceAll(s.mailConfig.PasswordResetURLTemplate, "{token}", token)
	// SMTP may include the entire message in an error: persist a fixed reason only.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	sendErr := s.sender.Send(sendCtx, mailer.Message{To: delivery.Email, ToName: delivery.DisplayName,
		Subject: "Worklane 密码重置", Body: fmt.Sprintf("%s，您好：\n请在 %d 分钟内打开以下链接设置新密码：\n%s\n链接仅可使用一次。如非本人操作，请忽略此邮件。\n", delivery.DisplayName, s.security.PasswordResetTokenTTLSeconds/60, link)})
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer recordCancel()
	if err := s.resets.RecordResetDelivery(recordCtx, delivery.ID, s.clk.Now(), sendErr == nil); err != nil {
		s.logger.Error("记录密码重置邮件投递结果失败")
	}
	return nil
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password string) error {
	if !s.security.PasswordResetEnabled || s.resets == nil {
		return domain.ErrPasswordResetUnavailable
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 64 {
		return domain.ErrValidation.WithField("token", "重置链接格式无效")
	}
	if _, fields := domain.NormalizeNewPassword(password, s.security.PasswordMinLength); len(fields) > 0 {
		return domain.ErrValidation.WithFields(fields)
	}
	hash := resetHash(token)
	userID, err := s.resets.ResetTokenUser(ctx, hash, s.clk.Now())
	if err != nil {
		return err
	}
	// Hash outside the transaction; unknown/expired tokens are rejected before Argon2.
	passwordHash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.ErrInternal.WithCause(err)
	}
	audit := apperr.NewAuditWrite(s.audit, domain.AuditEntry{ActorID: userID, Action: domain.AuditActionPasswordReset,
		ObjectType: domain.AuditObjectUser, ObjectID: userID, Changes: map[string]string{"source": "self_service"}})
	return s.resets.ConfirmPasswordReset(ctx, ResetConfirm{UserID: userID, Hash: hash, PasswordHash: passwordHash,
		Now: s.clk.Now(), Cooldown: time.Duration(s.security.PasswordResetCooldownSeconds) * time.Second, Audit: audit})
}
func resetHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
