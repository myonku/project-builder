//go:build live

package testsupport

// Opt-in provider acceptance test. Normal go test never compiles this file.
// Uses an explicitly named, disposable database, at most two SMTP messages,
// a 60-second minimum gap, and an exclusive marker preventing accidental reruns.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdmail "net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/myonku/project-builder/internal/app/auth"
	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/mail"
)

type liveMailReceipt struct {
	Subject   string    `json:"subject"`
	Started   time.Time `json:"started_utc"`
	ElapsedMS int64     `json:"elapsed_ms"`
	Accepted  bool      `json:"smtp_accepted"`
	Error     string    `json:"error,omitempty"`
}

type limitedLiveSender struct {
	sender    mail.Sender
	recipient string
	password  string
	lastEnd   time.Time
	blocked   bool
	receipts  []liveMailReceipt
	resetBody string // Raw reset link stays in memory; never serialized or logged.
}

func (s *limitedLiveSender) Send(ctx context.Context, message mail.Message) error {
	if message.To != s.recipient || s.blocked || len(s.receipts) >= 2 {
		return errors.New("live mail guard refused recipient, retry, or message budget")
	}
	if !s.lastEnd.IsZero() && time.Since(s.lastEnd) < time.Minute {
		return errors.New("live mail guard refused sending within 60 seconds")
	}
	receipt := liveMailReceipt{Subject: message.Subject, Started: time.Now().UTC()}
	err := s.sender.Send(ctx, message)
	s.lastEnd = time.Now()
	receipt.ElapsedMS = s.lastEnd.Sub(receipt.Started).Milliseconds()
	receipt.Accepted = err == nil
	if err != nil {
		s.blocked = true // Includes ambiguous post-DATA failures: never retry.
		receipt.Error = strings.ReplaceAll(err.Error(), s.password, "[redacted]")
		receipt.Error = regexp.MustCompile(`[0-9a-fA-F]{64}`).ReplaceAllString(receipt.Error, "[redacted-token]")
	}
	s.receipts = append(s.receipts, receipt)
	if err != nil {
		return errors.New(receipt.Error)
	}
	if message.Subject == "Worklane 密码重置" {
		s.resetBody = message.Body
	}
	return nil
}

func TestLiveSMTPTemplates(t *testing.T) {
	if os.Getenv("WORKLANE_LIVE_MAIL") != "1" {
		t.Skip("explicit WORKLANE_LIVE_MAIL=1 required")
	}
	dsn, err := url.Parse(os.Getenv(EnvDatabaseDSN))
	if err != nil || !strings.HasPrefix(strings.TrimPrefix(dsn.Path, "/"), "worklane_mail_live_") || dsn.Hostname() != "127.0.0.1" {
		t.Fatal("live acceptance requires an isolated local worklane_mail_live_* database")
	}
	to := os.Getenv("LIVE_MAIL_TO")
	address, err := stdmail.ParseAddress(to)
	if err != nil || address.Address != to {
		t.Fatal("LIVE_MAIL_TO must be one explicit recipient")
	}
	username, password := os.Getenv("MAIL_USERNAME"), os.Getenv("MAIL_PASSWORD")
	if username == "" || password == "" {
		t.Fatal("SMTP credentials must come from process environment")
	}
	out := os.Getenv("LIVE_MAIL_OUTPUT")
	if !filepath.IsAbs(out) {
		t.Fatal("LIVE_MAIL_OUTPUT must be an explicit absolute artifact directory")
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "sending.lock")); !os.IsNotExist(err) {
		t.Fatal("sending marker already exists; review prior attempts rather than rerunning")
	}

	h := NewHarness(t)
	h.Clock.Set(time.Now().UTC())
	actor := h.RegisterAndLogin(t, "live_mail_acceptance")
	ctx := context.Background()
	if _, err := h.Pool.Exec(ctx, `UPDATE users SET email=$2,display_name=$3 WHERE id=$1`, actor.UserID, to, "Worklane 邮件验收（隔离测试账号）"); err != nil {
		t.Fatal(err)
	}
	group := h.CreateGroup(t, actor, "真实邮件验收组")
	project := h.CreateProject(t, actor, group, "Worklane 真实邮件模板验收")
	task := h.CreateTask(t, actor, project, "真实邮件验收：中文主题与进度 40%", "2026-10-26", "2026-10-30")
	current := actor.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200).Data(t)
	actor.Client.Patch(URL("/tasks/"+task), map[string]any{"progress": 40, "expected_updated_at": current["updated_at"]}).RequireStatus(t, 200)
	// Match the normal frontend template, including both dynamic identifiers.
	taskURL := "http://localhost:5173/projects/{project_id}?taskId={task_id}"
	repo := reminders.NewRepository(h.Pool)
	if _, err := h.Pool.Exec(ctx, `DELETE FROM reminders`); err != nil {
		t.Fatal(err)
	}
	kinds := []string{domain.ReminderKindAssigned, domain.ReminderKindStartSoon, domain.ReminderKindDueSoon, domain.ReminderKindOverdue, domain.ReminderKindOwnerChanged}
	for _, kind := range kinds {
		id, err := h.IDs.UUID()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := repo.InsertIfAbsent(ctx, reminders.CreateParams{ID: id, TaskID: task, RecipientID: actor.UserID, RecipientEmail: to, Kind: kind, ScheduledFor: h.Clock.Now(), DedupeKey: "offline-preview:" + kind}); err != nil {
			t.Fatal(err)
		}
	}
	capture := &FakeSender{}
	preview := reminders.NewService(reminders.Options{Store: repo, Sender: capture, Clock: h.Clock, Logger: h.Logger, TaskURLTemplate: taskURL})
	previewReport, err := preview.Dispatch(ctx)
	if err != nil || previewReport.Sent != 5 {
		t.Fatal("five local reminder previews failed")
	}
	previews := capture.Messages()
	for _, message := range previews {
		for _, required := range []string{"提醒原因：", "项目：Worklane", "任务：", "负责人：", "计划周期：2026-10-26 ~ 2026-10-30", "状态：进行中", "进度：40%", "?taskId=" + task, "关闭邮件提醒"} {
			if !strings.Contains(message.Body, required) {
				t.Fatalf("preview missing required content: %s", required)
			}
		}
		if !utf8.ValidString(message.Subject+message.Body) || strings.Contains(message.Body, "{task_id}") {
			t.Fatal("invalid UTF-8 or unexpanded task URL")
		}
	}
	writeLiveJSON(t, filepath.Join(out, "reminder-previews.json"), previews)
	t.Log("OFFLINE: all five real application reminder templates rendered and validated; zero provider sends")

	smtpSender, err := mail.NewSMTP(mail.SMTPConfig{Host: "smtp.qiye.aliyun.com", Port: 465, Username: username, Password: password, FromAddress: username, FromName: "Worklane（真实邮件低频验收）", ImplicitTLS: true, AuthMethod: "login", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal("SMTP configuration rejected")
	}
	limited := &limitedLiveSender{sender: smtpSender, recipient: to, password: password}
	defer func() {
		// Persist acceptance metadata, not credentials or password-reset links.
		writeLiveJSON(t, filepath.Join(out, "smtp-receipts.json"), limited.receipts)
	}()
	marker, err := os.OpenFile(filepath.Join(out, "sending.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("exclusive sending marker could not be acquired")
	}
	_ = marker.Close()
	id, err := h.IDs.UUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.InsertIfAbsent(ctx, reminders.CreateParams{ID: id, TaskID: task, RecipientID: actor.UserID, RecipientEmail: to, Kind: domain.ReminderKindDueSoon, ScheduledFor: h.Clock.Now(), DedupeKey: "live-provider:one-reminder"}); err != nil {
		t.Fatal(err)
	}
	liveReminders := reminders.NewService(reminders.Options{Store: repo, Sender: limited, Clock: h.Clock, Logger: h.Logger, TaskURLTemplate: taskURL, BatchSize: 1})
	liveReport, err := liveReminders.Dispatch(ctx)
	if err != nil || liveReport.Sent != 1 || liveReport.Failed != 0 || len(limited.receipts) != 1 || !limited.receipts[0].Accepted {
		t.Fatal("live reminder was not confirmed by SMTP; stopping without retries; see redacted receipt")
	}
	record, err := repo.Find(ctx, id)
	if err != nil || record.Status != domain.ReminderStatusSent || record.SentAt == nil {
		t.Fatal("real reminder acceptance was not persisted")
	}
	t.Logf("LIVE 1/2: SMTP accepted task reminder at %s; waiting at least 60 seconds before reset template", limited.receipts[0].Started.Format(time.RFC3339))
	writeLiveJSON(t, filepath.Join(out, "smtp-receipts.json"), limited.receipts)

	firstCompleted := limited.lastEnd
	// One bounded delay, no sending loop and no retry scheduler.
	timer := time.NewTimer(61 * time.Second)
	defer timer.Stop()
	<-timer.C
	h.Clock.Set(time.Now().UTC())
	mailConfig := h.Config.Mail
	mailConfig.Enabled = true
	resetService := auth.NewService(auth.Options{Users: h.Users, Resets: h.Users, Sender: limited, Mail: mailConfig, Hasher: h.Hasher, IDs: h.IDs, Clock: h.Clock, Logger: h.Logger, Security: h.Config.Security, Audit: h.Audit})
	if err := resetService.RequestPasswordReset(ctx, to); err != nil {
		t.Fatal("reset request failed; no retry")
	}
	if len(limited.receipts) != 2 || !limited.receipts[1].Accepted {
		t.Fatal("reset SMTP acceptance failed; no retry; see redacted receipt")
	}
	var sent, failed bool
	if err := h.Pool.QueryRow(ctx, `SELECT sent_at IS NOT NULL,failed_at IS NOT NULL FROM password_reset_tokens WHERE user_id=$1`, actor.UserID).Scan(&sent, &failed); err != nil || !sent || failed {
		t.Fatal("reset delivery status was not persisted")
	}
	body := limited.resetBody
	if !strings.Contains(body, "30 分钟") || !strings.Contains(body, "链接仅可使用一次") || !strings.Contains(body, "如非本人操作") || !utf8.ValidString(body) {
		t.Fatal("reset template missing required content")
	}
	match := regexp.MustCompile(`token=([0-9a-f]{64})`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("reset template link invalid")
	}
	writeLiveJSON(t, filepath.Join(out, "reset-preview-redacted.json"), map[string]string{"subject": "Worklane 密码重置", "body": strings.ReplaceAll(body, match[1], "[REDACTED-TEST-TOKEN]")})
	if err := resetService.ConfirmPasswordReset(ctx, match[1], "isolated live acceptance replacement password"); err != nil {
		t.Fatal("isolated reset token could not be consumed")
	}
	if err := resetService.ConfirmPasswordReset(ctx, match[1], "isolated live acceptance replacement password"); err == nil {
		t.Fatal("reset token was reusable")
	}
	actor.Client.Get(URL("/auth/me")).RequireStatus(t, 401)
	limited.resetBody = ""
	writeLiveJSON(t, filepath.Join(out, "result.json"), map[string]any{"reminder_templates_previewed": 5, "smtp_messages_accepted": 2, "minimum_gap_seconds": 60, "actual_gap_seconds": limited.receipts[1].Started.Sub(firstCompleted).Seconds(), "reset_single_use_verified": true, "old_session_revoked": true, "inbox_delivery_confirmed": false, "task_url_template": taskURL, "reset_link_consumed": true})
	t.Log("LIVE 2/2: SMTP accepted password-reset template; sent_at persisted, isolated token consumed once, old session rejected; no further sends")
}

func writeLiveJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal("cannot encode redacted live acceptance artifact")
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(fmt.Errorf("cannot save live acceptance artifact: %w", err))
	}
}

// Verify guard behavior using an in-memory sender; this test never contacts SMTP.
func TestLiveMailGuard(t *testing.T) {
	capture := &FakeSender{}
	guard := &limitedLiveSender{sender: capture, recipient: "recipient@example.test", password: "dummy-secret"}
	message := mail.Message{To: guard.recipient, Subject: "guard test"}
	ctx := context.Background()
	if err := guard.Send(ctx, mail.Message{To: "other@example.test"}); err == nil || len(capture.Messages()) != 0 {
		t.Fatal("unexpected recipient reached transport")
	}
	if err := guard.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := guard.Send(ctx, message); err == nil || len(capture.Messages()) != 1 {
		t.Fatal("rate guard allowed a second immediate transport call")
	}
	guard.lastEnd = guard.lastEnd.Add(-61 * time.Second) // Simulated elapsed time; no real waiting/sending.
	if err := guard.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	guard.lastEnd = guard.lastEnd.Add(-61 * time.Second)
	if err := guard.Send(ctx, message); err == nil || len(capture.Messages()) != 2 {
		t.Fatal("message budget allowed a third transport call")
	}
	failed := &FakeSender{}
	failed.FailWith(errors.New("SMTP failure with dummy-secret"))
	blocked := &limitedLiveSender{sender: failed, recipient: message.To, password: "dummy-secret"}
	if err := blocked.Send(ctx, message); err == nil || strings.Contains(err.Error(), "dummy-secret") {
		t.Fatal("transport failure was not redacted")
	}
	failed.FailWith(nil)
	blocked.lastEnd = blocked.lastEnd.Add(-61 * time.Second)
	if err := blocked.Send(ctx, message); err == nil || len(failed.Messages()) != 0 {
		t.Fatal("transport failure did not latch the no-retry guard")
	}
}
