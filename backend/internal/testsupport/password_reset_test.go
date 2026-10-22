package testsupport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/myonku/project-builder/internal/app/auth"
	"github.com/myonku/project-builder/internal/domain"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetEmail(t *testing.T, h *Harness, id string) string {
	t.Helper()
	var email string
	if err := h.Pool.QueryRow(context.Background(), `SELECT email FROM users WHERE id=$1`, id).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}
func resetRequest(t *testing.T, h *Harness, email string) {
	t.Helper()
	res := h.Client(t).Post(URL("/auth/password-reset/request"), map[string]any{"email": email}).RequireStatus(t, 202)
	if Str(res.Data(t), "status") != "accepted" {
		t.Fatal("bad accepted envelope")
	}
}
func resetToken(t *testing.T, h *Harness) string {
	t.Helper()
	messages := h.Mail.Messages()
	if len(messages) == 0 {
		t.Fatal("no mail")
	}
	m := regexp.MustCompile(`token=([0-9a-f]{64})`).FindStringSubmatch(messages[len(messages)-1].Body)
	if len(m) != 2 {
		t.Fatal("no token")
	}
	return m[1]
}
func resetConfirm(t *testing.T, h *Harness, token string) Response {
	t.Helper()
	return h.Client(t).Post(URL("/auth/password-reset/confirm"), map[string]any{"token": token, "new_password": "new secure password"})
}
func TestPasswordResetLifecycle(t *testing.T) {
	h := NewHarness(t)
	user := h.RegisterAndLogin(t, "reset_lifecycle")
	email := resetEmail(t, h, user.UserID)
	resetRequest(t, h, "absent@example.com")
	if len(h.Mail.Messages()) != 0 {
		t.Fatal("unknown email sent")
	}
	resetRequest(t, h, strings.ToUpper(email))
	old := resetToken(t, h)
	resetRequest(t, h, email)
	token := resetToken(t, h)
	resetConfirm(t, h, old).RequireStatus(t, 422).RequireCode(t, "password_reset_token_invalid")
	h.Client(t).Post(URL("/auth/password-reset/confirm"), map[string]any{"token": token, "new_password": "short"}).RequireStatus(t, 422)
	sum := sha256.Sum256([]byte(token))
	var stored string
	if err := h.Pool.QueryRow(context.Background(), `SELECT token_hash FROM password_reset_tokens WHERE consumed_at IS NULL`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hex.EncodeToString(sum[:]) || stored == token {
		t.Fatal("raw token stored")
	}
	resetConfirm(t, h, token).RequireStatus(t, 204)
	user.Client.Get(URL("/auth/me")).RequireStatus(t, 401)
	resetConfirm(t, h, token).RequireStatus(t, 422)
	h.Client(t).Post(URL("/auth/login"), map[string]any{"identifier": user.Username, "password": SharedPassword}).RequireStatus(t, 401)
	h.Client(t).Post(URL("/auth/login"), map[string]any{"identifier": user.Username, "password": "new secure password"}).RequireStatus(t, 200)
	var changes string
	if err := h.Pool.QueryRow(context.Background(), `SELECT changes::text FROM activity_logs WHERE object_id=$1 AND action='password_reset'`, user.UserID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(changes, "self_service") || strings.Contains(changes, token) {
		t.Fatal("bad audit")
	}
	count := len(h.Mail.Messages())
	resetRequest(t, h, email)
	if len(h.Mail.Messages()) != count {
		t.Fatal("cooldown sent mail")
	}
	h.Clock.Advance(7 * 24 * time.Hour)
	resetRequest(t, h, email)
	if len(h.Mail.Messages()) != count+1 {
		t.Fatal("cooldown did not expire")
	}
}
func TestPasswordResetQuotaAndFailure(t *testing.T) {
	h := NewHarness(t)
	user := h.RegisterAndLogin(t, "reset_quota")
	email := resetEmail(t, h, user.UserID)
	h.Mail.FailWith(errors.New("sensitive SMTP error"))
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.Auth.RequestPasswordReset(context.Background(), email); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatal("request failed")
	}
	var count, active, failed int
	err := h.Pool.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER (WHERE consumed_at IS NULL),count(*) FILTER (WHERE failed_at IS NOT NULL AND last_error='SMTP delivery failed') FROM password_reset_tokens`).Scan(&count, &active, &failed)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 || active != 1 || failed != 5 {
		t.Fatalf("count=%d active=%d failed=%d", count, active, failed)
	}
	h.Mail.FailWith(nil)
	resetRequest(t, h, email)
	if len(h.Mail.Messages()) != 0 {
		t.Fatal("over quota sent")
	}
	h.Clock.Advance(7 * 24 * time.Hour)
	resetRequest(t, h, email)
	if len(h.Mail.Messages()) != 1 {
		t.Fatal("window did not expire")
	}
}
func TestPasswordResetConcurrentConfirm(t *testing.T) {
	h := NewHarness(t)
	user := h.RegisterAndLogin(t, "reset_parallel")
	resetRequest(t, h, resetEmail(t, h, user.UserID))
	token := resetToken(t, h)
	var wg sync.WaitGroup
	var successes, failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.Auth.ConfirmPasswordReset(context.Background(), token, "parallel new password")
			if err == nil {
				successes.Add(1)
			} else if domain.AsError(err).Code != domain.CodePasswordResetTokenInvalid {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || failures.Load() != 0 {
		t.Fatalf("successes=%d unexpected=%d", successes.Load(), failures.Load())
	}
}
func TestPasswordResetExpiryAndEmailRevocation(t *testing.T) {
	for _, mode := range []string{"expiry", "email"} {
		t.Run(mode, func(t *testing.T) {
			h := NewHarness(t)
			user := h.RegisterAndLogin(t, "reset_revoke")
			resetRequest(t, h, resetEmail(t, h, user.UserID))
			token := resetToken(t, h)
			if mode == "expiry" {
				h.Clock.Advance(30 * time.Minute)
			} else {
				current := user.Client.Get(URL("/auth/me")).RequireStatus(t, 200)
				user.Client.Patch(URL("/users/"+user.UserID), map[string]any{"email": "replacement@example.com", "expected_updated_at": Str(current.Data(t), "updated_at")}).RequireStatus(t, 200)
			}
			resetConfirm(t, h, token).RequireStatus(t, 422).RequireCode(t, "password_reset_token_invalid")
		})
	}
}
func TestPasswordResetHTTPBoundaries(t *testing.T) {
	h := NewHarness(t)
	h.Client(t).Post(URL("/auth/password-reset/request"), map[string]any{"email": "invalid"}).RequireStatus(t, 422)
	h.Client(t).Post(URL("/auth/password-reset/request"), map[string]any{"email": "x@example.com", "extra": true}).RequireStatus(t, 422)
	h.Client(t).Post(URL("/auth/password-reset/confirm"), map[string]any{"token": "bad", "new_password": "good password"}).RequireStatus(t, 422)
	h.Client(t).Post(URL("/auth/password-reset/confirm/extra"), map[string]any{}).RequireStatus(t, 404)
}

func TestPasswordResetAdminAndDisableRevocation(t *testing.T) {
	for _, mode := range []string{"admin", "disable"} {
		t.Run(mode, func(t *testing.T) {
			h := NewHarness(t)
			user := h.RegisterAndLogin(t, "reset_target")
			admin := h.RegisterAndLogin(t, "reset_admin")
			promoteToAdmin(t, h, admin.UserID)
			email := resetEmail(t, h, user.UserID)
			resetRequest(t, h, email)
			token := resetToken(t, h)
			current := admin.Client.Get(URL("/users/"+user.UserID)).RequireStatus(t, 200)
			body := map[string]any{"expected_updated_at": Str(current.Data(t), "updated_at")}
			path := "/users/" + user.UserID + "/disable"
			if mode == "admin" {
				path = "/users/" + user.UserID + "/password"
				body["new_password"] = "admin new password"
			}
			admin.Client.Post(URL(path), body).RequireStatus(t, 200)
			resetConfirm(t, h, token).RequireStatus(t, 422).RequireCode(t, "password_reset_token_invalid")
			user.Client.Get(URL("/auth/me")).RequireStatus(t, 401)
			resetRequest(t, h, email)
			if mode == "disable" && len(h.Mail.Messages()) != 1 {
				t.Fatal("disabled user received mail")
			}
		})
	}
}

func TestPasswordResetAvailability(t *testing.T) {
	h := NewHarness(t)
	user := h.RegisterAndLogin(t, "reset_availability")
	email := resetEmail(t, h, user.UserID)
	resetRequest(t, h, email)
	token := resetToken(t, h)
	cfg := h.Config.Security
	cfg.PasswordResetEnabled = false
	disabled := auth.NewService(auth.Options{Resets: h.Users, Security: cfg})
	if domain.AsError(disabled.RequestPasswordReset(context.Background(), email)).Code != domain.CodePasswordResetUnavailable {
		t.Fatal("disabled request allowed")
	}
	if domain.AsError(disabled.ConfirmPasswordReset(context.Background(), token, "new password")).Code != domain.CodePasswordResetUnavailable {
		t.Fatal("disabled confirm allowed")
	}
	mailCfg := h.Config.Mail
	mailCfg.Enabled = false
	cfg.PasswordResetEnabled = true
	offline := auth.NewService(auth.Options{Users: h.Users, Resets: h.Users, Hasher: h.Hasher, IDs: h.IDs, Clock: h.Clock, Security: cfg, Mail: mailCfg, Sender: h.Mail})
	if domain.AsError(offline.RequestPasswordReset(context.Background(), email)).Code != domain.CodePasswordResetUnavailable {
		t.Fatal("offline request allowed")
	}
	if err := offline.ConfirmPasswordReset(context.Background(), token, "new password"); err != nil {
		t.Fatal("mail shutdown invalidated issued link", err)
	}
}
