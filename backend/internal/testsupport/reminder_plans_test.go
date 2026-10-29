package testsupport

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/myonku/project-builder/internal/database"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/platform/mail"
)

func p02Fixture(t *testing.T, end string) (*Harness, Authenticated, string, string) {
	t.Helper()
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "p02owner")
	group := h.CreateGroup(t, owner, "P02")
	project := h.CreateProject(t, owner, group, "P02")
	task := h.CreateTask(t, owner, project, "P02 task", "2026-10-01", end)
	return h, owner, project, task
}
func p02SQL(t *testing.T, h *Harness, task string, at time.Time, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT set_config('worklane.reminder_now',$1,true)`, at.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM projects WHERE id=(SELECT project_id FROM tasks WHERE id=$1) FOR UPDATE`, task); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, query, append([]any{task}, args...)...); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func p02Plan(t *testing.T, h *Harness, task, kind string) (string, time.Time, string) {
	t.Helper()
	var id, status string
	var at time.Time
	err := h.Pool.QueryRow(context.Background(), `SELECT id,scheduled_for,status FROM reminders WHERE task_id=$1 AND kind=$2 AND NOT legacy ORDER BY cycle DESC,created_at DESC,id DESC LIMIT 1`, task, kind).Scan(&id, &at, &status)
	if err != nil {
		t.Fatal(err)
	}
	return id, at, status
}
func p02Render(i reminders.DispatchItem) mail.Message {
	return mail.Message{To: i.Recipient.Email, Subject: i.Candidate.TaskName, Body: i.Candidate.EndDate.Format("2006-01-02")}
}
func p02Authorize(t *testing.T, h *Harness, id string, at time.Time) *reminders.Delivery {
	t.Helper()
	d, err := reminders.NewRepository(h.Pool).Authorize(context.Background(), id, at, time.Minute, p02Render)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("expected committed authorization")
	}
	return d
}

func TestP02PersistAndReschedule(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-13")
	repo := reminders.NewRepository(h.Pool)
	now := h.Clock.Now()
	id, oldTime, status := p02Plan(t, h, task, "task_due_soon")
	if status != "pending" || !oldTime.After(now) {
		t.Fatal("future schedule missing", status, oldTime)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-16' WHERE id=$1`)
	newer, newTime, _ := p02Plan(t, h, task, "task_due_soon")
	if newer == id || !newTime.After(oldTime) {
		t.Fatal("old plan not replaced")
	}
	if d, err := repo.Authorize(context.Background(), id, oldTime.Add(time.Minute), time.Minute, p02Render); err != nil || d != nil {
		t.Fatal("old candidate authorized", d, err)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-13' WHERE id=$1`)
	var start, due int
	if err := h.Pool.QueryRow(context.Background(), `SELECT reminder_start_cycle,reminder_due_cycle FROM tasks WHERE id=$1`, task).Scan(&start, &due); err != nil {
		t.Fatal(err)
	}
	if start != 1 || due != 3 {
		t.Fatal("independent cycles", start, due)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date=end_date,name='ordinary' WHERE id=$1`)
	if err := h.Pool.QueryRow(context.Background(), `SELECT reminder_due_cycle FROM tasks WHERE id=$1`, task).Scan(&start); err != nil || start != 3 {
		t.Fatal(start, err)
	}
	var count int
	_ = h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders WHERE task_id=$1 AND kind='task_due_soon' AND status='pending'`, task).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate pending", count)
	}
}

func TestP02SuppressionSurvivesTomorrow(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	now := h.Clock.Now()
	repo := reminders.NewRepository(h.Pool)
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	if err := repo.Settle(context.Background(), d, "sent", now, ""); err != nil {
		t.Fatal(err)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-09' WHERE id=$1`)
	_, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "suppressed" {
		t.Fatal(state)
	}
	tomorrow := now.Add(24 * time.Hour)
	p02SQL(t, h, task, tomorrow, `UPDATE tasks SET name='tomorrow' WHERE id=$1`)
	if err := repo.ReconcileTask(context.Background(), task, tomorrow); err != nil {
		t.Fatal(err)
	}
	_, _, state = p02Plan(t, h, task, "task_overdue")
	if state != "suppressed" {
		t.Fatal("suppression revived", state)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-15' WHERE id=$1`)
	_, when, state := p02Plan(t, h, task, "task_overdue")
	if state != "pending" || !when.After(now) {
		t.Fatal("future suppressed", state, when)
	}
}

func TestP02NaturalOverdueKeepsNine(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-11")
	repo := reminders.NewRepository(h.Pool)
	overdue, when, _ := p02Plan(t, h, task, "task_overdue")
	before := when.Add(-6 * time.Hour)
	p02SQL(t, h, task, before, `UPDATE tasks SET name='midnight edit' WHERE id=$1`)
	if err := repo.ReconcileTask(context.Background(), task, before); err != nil {
		t.Fatal(err)
	}
	_, after, _ := p02Plan(t, h, task, "task_overdue")
	if !after.Equal(when) {
		t.Fatal("ordinary edit advanced overdue", when, after)
	}
	due, _, _ := p02Plan(t, h, task, "task_due_soon")
	if d, err := repo.Authorize(context.Background(), due, before, time.Minute, p02Render); err != nil || d != nil {
		t.Fatal("expired due reminder", d, err)
	}
	if d, err := repo.Authorize(context.Background(), overdue, before, time.Minute, p02Render); err != nil || d != nil {
		t.Fatal("early overdue", d, err)
	}
	p02Authorize(t, h, overdue, when)
}

func TestP02CancelRestoreKeepsCycle(t *testing.T) {
	h, _, project, task := p02Fixture(t, "2026-10-10")
	now := h.Clock.Now()
	repo := reminders.NewRepository(h.Pool)
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	if err := repo.Settle(context.Background(), d, "sent", now, ""); err != nil {
		t.Fatal(err)
	}
	p02SQL(t, h, task, now, `UPDATE tasks SET status='cancelled' WHERE id=$1`)
	p02SQL(t, h, task, now, `UPDATE tasks SET status='in_progress',deleted_at=now() WHERE id=$1`)
	p02SQL(t, h, task, now, `UPDATE tasks SET deleted_at=NULL WHERE id=$1`)
	_, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "sent" {
		t.Fatal("restore reset sent fact", state)
	}
	p02SQL(t, h, task, now, `UPDATE projects SET status='archived',archived_at=now() WHERE id=(SELECT project_id FROM tasks WHERE id=$1)`)
	p02SQL(t, h, task, now, `UPDATE projects SET status='in_progress',archived_at=NULL WHERE id=(SELECT project_id FROM tasks WHERE id=$1)`)
	var cycle int
	_ = h.Pool.QueryRow(context.Background(), `SELECT reminder_due_cycle FROM tasks WHERE id=$1`, task).Scan(&cycle)
	if cycle != 1 {
		t.Fatal("restore changed cycle", project, cycle)
	}
}

func TestP02MultipleDispatchers(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := h.Reminders.Dispatch(context.Background()); failures <- err }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM reminder_delivery_attempts WHERE task_id=$1 AND kind='task_overdue' AND status='sent'`, task).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate actual send", n)
	}
	if len(h.Mail.Messages()) != 2 {
		t.Fatal("expected assignment and overdue once each", len(h.Mail.Messages()))
	}
}

func TestP02AuthorizationWinsReschedule(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	now := h.Clock.Now()
	repo := reminders.NewRepository(h.Pool)
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-09',name='new name' WHERE id=$1`)
	newer, _, _ := p02Plan(t, h, task, "task_overdue")
	if newer == id {
		t.Fatal("no new cycle")
	}
	if next, err := repo.Authorize(context.Background(), newer, now, time.Minute, p02Render); err != nil || next != nil {
		t.Fatal("in-flight gate bypass", next, err)
	}
	if d.Message.Subject != "P02 task" || d.Message.Body != "2026-10-10" {
		t.Fatal("snapshot mutated", d.Message)
	}
	if err := repo.Settle(context.Background(), d, "sent", now, ""); err != nil {
		t.Fatal(err)
	}
	_, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "suppressed" {
		t.Fatal("new due plan not consumed", state)
	}
}

func TestP02UnknownIsolationAndResolution(t *testing.T) {
	h, owner, _, task := p02Fixture(t, "2026-10-10")
	repo := reminders.NewRepository(h.Pool)
	now := h.Clock.Now()
	promoteToAdmin(t, h, owner.UserID)
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	if err := repo.Settle(context.Background(), d, "unknown", now, "final response lost"); err != nil {
		t.Fatal(err)
	}
	plain := h.RegisterAndLogin(t, "p02_non_admin")
	if err := repo.ResolveUnknown(context.Background(), d.Token, plain.UserID, "not_accepted", "fixture", nil, true, now); err == nil {
		t.Fatal("non-admin resolved unknown delivery")
	}
	owner.Client.Post(URL("/reminders/"+id+"/retry"), nil).RequireStatus(t, 409)
	p02SQL(t, h, task, now, `UPDATE tasks SET end_date='2026-10-09' WHERE id=$1`)
	newer, _, _ := p02Plan(t, h, task, "task_overdue")
	for _, at := range []time.Time{now, now.Add(24 * time.Hour)} {
		if next, err := repo.Authorize(context.Background(), newer, at, time.Minute, p02Render); err != nil || next != nil {
			t.Fatal("unknown gate bypass", next, err)
		}
	}
	if err := repo.ResolveUnknown(context.Background(), d.Token, owner.UserID, "accepted", "receipt", &now, false, now); err == nil {
		t.Fatal("missing stopped confirmation accepted")
	}
	release, ok, err := repo.BeginDispatch(context.Background())
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := repo.ResolveUnknown(context.Background(), d.Token, owner.UserID, "accepted", "receipt", &now, true, now); err == nil {
		t.Fatal("active worker was not blocked")
	}
	release()
	if err := repo.ResolveUnknown(context.Background(), d.Token, owner.UserID, "accepted", "receipt", &now, true, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Settle(context.Background(), d, "failed", now, "late worker"); err == nil {
		t.Fatal("late result overwrote resolution")
	}
	_, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "suppressed" {
		t.Fatal(state)
	}
}

func TestP02UnknownTimeoutAndSafeFailure(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	repo := reminders.NewRepository(h.Pool)
	now := h.Clock.Now()
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	if err := repo.Expire(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Find(context.Background(), id)
	if err != nil || rec.Status != "unknown" || rec.CanRetry {
		t.Fatal(rec, err)
	}
	if next, err := repo.Authorize(context.Background(), id, now.Add(3*time.Minute), time.Minute, p02Render); err != nil || next != nil {
		t.Fatal("timeout reclaimed", next, err)
	}
	// A late, positively observed receipt may settle its own token before manual resolution.
	if err := repo.Settle(context.Background(), d, "sent", now.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
}

func TestP02SettlementFailureNeverResends(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	ctx := context.Background()
	_, err := h.Pool.Exec(ctx, `CREATE FUNCTION p02_fail_settle() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='sent' THEN RAISE EXCEPTION 'injected settlement failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER p02_fail_settle BEFORE UPDATE ON reminders FOR EACH ROW EXECUTE FUNCTION p02_fail_settle()`)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_, _ = h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS p02_fail_settle ON reminders; DROP FUNCTION IF EXISTS p02_fail_settle()`)
	}
	t.Cleanup(cleanup)
	if _, err = h.Reminders.Dispatch(ctx); err == nil {
		t.Fatal("fault did not propagate")
	}
	cleanup()
	sent := len(h.Mail.Messages())
	if sent != 1 {
		t.Fatal("expected exactly one call before failure", sent)
	}
	h.Clock.Set(h.Clock.Now().Add(3 * time.Minute))
	if _, err = h.Reminders.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	// The independent other type may send, but the failed settlement's type stays isolated.
	var n int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminder_delivery_attempts WHERE task_id=$1 AND status='unknown'`, task).Scan(&n)
	if n != 1 {
		t.Fatal("not quarantined", n)
	}
	for _, m := range h.Mail.Messages()[sent:] {
		if m.Subject == h.Mail.Messages()[0].Subject {
			t.Fatal("blind SMTP resend")
		}
	}
}

func TestP02SyncFailureRollsBackBusiness(t *testing.T) {
	h, owner, project, _ := p02Fixture(t, "2026-10-13")
	ctx := context.Background()
	_, err := h.Pool.Exec(ctx, `CREATE FUNCTION p02_fail_sync() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected plan failure'; END $$;
 CREATE TRIGGER p02_fail_sync BEFORE INSERT ON reminders FOR EACH ROW EXECUTE FUNCTION p02_fail_sync()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS p02_fail_sync ON reminders; DROP FUNCTION IF EXISTS p02_fail_sync()`)
	})
	owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": "must rollback", "start_date": "2026-10-12", "end_date": "2026-10-13"}).RequireStatus(t, 500)
	var n int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE name='must rollback'`).Scan(&n)
	if n != 0 {
		t.Fatal("business committed without plan")
	}
}

func TestP02QualificationTimezoneAndEventIdentity(t *testing.T) {
	h, owner, project, task := p02Fixture(t, "2026-10-13")
	ctx := context.Background()
	member := h.RegisterAndLogin(t, "p02member")
	h.AddProjectMember(t, owner, project, member.UserID)
	add := func() {
		owner.Client.Post(URL("/tasks/"+task+"/members"), map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)
	}
	remove := func() { owner.Client.Delete(URL("/tasks/"+task+"/members/"+member.UserID), nil).RequireStatus(t, 204) }
	add()
	var first string
	_ = h.Pool.QueryRow(ctx, `SELECT event_id FROM reminders WHERE task_id=$1 AND recipient_id=$2 AND kind='task_assigned' AND status='pending'`, task, member.UserID).Scan(&first)
	remove()
	add()
	var second string
	_ = h.Pool.QueryRow(ctx, `SELECT event_id FROM reminders WHERE task_id=$1 AND recipient_id=$2 AND kind='task_assigned' AND status='pending'`, task, member.UserID).Scan(&second)
	if first == second || first == "" || second == "" {
		t.Fatal("event identity reused", first, second)
	}
	setReminderEnabled(t, member, false)
	var n int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE task_id=$1 AND recipient_id=$2 AND status='pending'`, task, member.UserID).Scan(&n)
	if n != 0 {
		t.Fatal("disabled plans retained", n)
	}
	setReminderEnabled(t, member, true)
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE task_id=$1 AND recipient_id=$2 AND kind='task_assigned' AND status='pending'`, task, member.UserID).Scan(&n)
	if n != 0 {
		t.Fatal("old assignment revived")
	}
	// Current time plans use new zone and address, retaining immutable historical attempts.
	data := member.Client.Get(URL("/users/"+member.UserID)).RequireStatus(t, 200).Data(t)
	member.Client.Patch(URL("/users/"+member.UserID), map[string]any{"timezone": "America/New_York", "email": "p02changed@example.test", "expected_updated_at": data["updated_at"]}).RequireStatus(t, 200)
	var zone, address string
	err := h.Pool.QueryRow(ctx, `SELECT plan_timezone,recipient_email FROM reminders WHERE task_id=$1 AND recipient_id=$2 AND kind='task_due_soon' AND status='pending'`, task, member.UserID).Scan(&zone, &address)
	if err != nil || zone != "America/New_York" || address != "p02changed@example.test" {
		t.Fatal(zone, address, err)
	}
}

func TestP02InitializationIsExplicitAndRepeatable(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	ctx := context.Background()
	repo := reminders.NewRepository(h.Pool)
	now := h.Clock.Now()
	if _, err := h.Pool.Exec(ctx, `DELETE FROM reminders WHERE task_id=$1`, task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Initialize(ctx, now, false, true); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE task_id=$1`, task).Scan(&n)
	if n != 0 {
		t.Fatal("dry run wrote data")
	}
	for range 2 {
		if _, err := repo.Initialize(ctx, now, false, false); err != nil {
			t.Fatal(err)
		}
	}
	id, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "cancelled" {
		t.Fatal("initial overdue backlog released", state)
	}
	if _, err := repo.Initialize(ctx, now, true, false); err != nil {
		t.Fatal(err)
	}
	newid, _, state := p02Plan(t, h, task, "task_overdue")
	if newid != id || state != "pending" {
		t.Fatal("idempotent release", id, newid, state)
	}
}

func TestP02UnknownFailureClassification(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(fmt.Sprint(safe), func(t *testing.T) {
			h, _, _, task := p02Fixture(t, "2026-10-10")
			failure := errors.New("SMTP result lost")
			if safe {
				failure = mail.NotAccepted(failure)
			}
			h.Mail.FailWith(failure)
			report, err := h.Reminders.Dispatch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, _, state := p02Plan(t, h, task, "task_overdue")
			want := "unknown"
			if safe {
				want = "failed"
			}
			if state != want {
				t.Fatal(state, want, report)
			}
		})
	}
}

func TestP02MultipleActualDaysAreDeduplicated(t *testing.T) {
	h, _, _, task := p02Fixture(t, "2026-10-10")
	repo := reminders.NewRepository(h.Pool)
	now := h.Clock.Now()
	id, _, _ := p02Plan(t, h, task, "task_overdue")
	d := p02Authorize(t, h, id, now)
	if err := repo.Settle(context.Background(), d, "sent", now, ""); err != nil {
		t.Fatal(err)
	}
	later := now.Add(24 * time.Hour)
	p02SQL(t, h, task, later, `UPDATE tasks SET end_date='2026-10-09' WHERE id=$1`)
	id, _, state := p02Plan(t, h, task, "task_overdue")
	if state != "pending" {
		t.Fatal("new day disallowed new cycle", state)
	}
	d = p02Authorize(t, h, id, later)
	if err := repo.Settle(context.Background(), d, "sent", later, ""); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Find(context.Background(), id)
	if err != nil || len(rec.Deliveries) != 1 || !strings.Contains(string(rec.Deliveries[0].Snapshot), "2026-10-09") {
		t.Fatal("missing snapshot", rec, err)
	}
}

func TestP02LegacyUpgradeAndRollback(t *testing.T) {
	h, owner, _, task := p02Fixture(t, "2026-10-10")
	ctx := context.Background()
	now := h.Clock.Now()
	migrator, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrator.Up(); migrator.Close() }()
	// Exercise the real previous schema with historical evidence, not a mock migration.
	if err = migrator.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(ctx, `DELETE FROM reminders WHERE task_id=$1`, task); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct{ kind, status string }{{"task_overdue", "sent"}, {"task_due_soon", "failed"}, {"task_start_soon", "pending"}, {"task_assigned", "skipped"}} {
		_, err = h.Pool.Exec(ctx, `INSERT INTO reminders(id,task_id,recipient_id,recipient_email,kind,scheduled_for,dedupe_key,status,sent_at,last_error,attempts)
   VALUES(gen_random_uuid(),$1,$2,'old@example.test',$3::text,$4,$3::text||':legacy',$5::text,CASE WHEN $5::text='sent' THEN $4::timestamptz ELSE NULL END,'old diagnostic',1)`, task, owner.UserID, seed.kind, now, seed.status)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = migrator.Up(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE task_id=$1`, task).Scan(&n); err != nil || n != 4 {
		t.Fatal("history lost", n, err)
	}
	if err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminder_delivery_attempts WHERE task_id=$1 AND status IN ('sent','unknown')`, task).Scan(&n); err != nil || n != 2 {
		t.Fatal("legacy attempts not imported", n, err)
	}
	repo := reminders.NewRepository(h.Pool)
	for range 2 {
		if _, err = repo.Initialize(ctx, now, false, false); err != nil {
			t.Fatal(err)
		}
	}
	var active string
	if err = h.Pool.QueryRow(ctx, `SELECT active_attempt FROM reminder_delivery_guards WHERE task_id=$1 AND kind='task_due_soon'`, task).Scan(&active); err != nil || active == "" {
		t.Fatal("legacy uncertain failure unguarded", err)
	}
	var preserved string
	if err = h.Pool.QueryRow(ctx, `SELECT snapshot->>'original_error' FROM reminder_delivery_attempts WHERE id=$1`, active).Scan(&preserved); err != nil || preserved != "old diagnostic" {
		t.Fatal(preserved, err)
	}
	// Export facts before downgrading: the old binary cannot interpret the new lifecycle.
	var exported []byte
	if err = h.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(a)) FROM reminder_delivery_attempts a WHERE task_id=$1`, task).Scan(&exported); err != nil || len(exported) == 0 {
		t.Fatal("no rollback evidence", err)
	}
	if err = migrator.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if err = migrator.Up(); err != nil {
		t.Fatal(err)
	}
	version, dirty, err := migrator.Version()
	if err != nil || version != 11 || dirty {
		t.Fatal(version, dirty, err)
	}
}

func TestP02DispatchBatchAndThrottle(t *testing.T) {
	h, _, _, _ := p02Fixture(t, "2026-10-10")
	service := reminders.NewService(reminders.Options{Store: reminders.NewRepository(h.Pool), Sender: h.Mail, Clock: h.Clock, BatchSize: 1})
	report, err := service.Dispatch(context.Background())
	if err != nil || report.Sent != 1 {
		t.Fatal("batch bound", report, err)
	}
	if _, err = service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	// New task supplies two independently due kinds; interval applies between sends.
	owner := h.RegisterAndLogin(t, "throttleowner")
	group := h.CreateGroup(t, owner, "throttle")
	project := h.CreateProject(t, owner, group, "throttle")
	h.CreateTask(t, owner, project, "throttle", "2026-10-01", "2026-10-10")
	service = reminders.NewService(reminders.Options{Store: reminders.NewRepository(h.Pool), Sender: h.Mail, Clock: h.Clock, BatchSize: 2, SendInterval: 30 * time.Millisecond})
	start := time.Now()
	report, err = service.Dispatch(context.Background())
	if err != nil || report.Sent != 2 || time.Since(start) < 30*time.Millisecond {
		t.Fatal("throttle", report, err, time.Since(start))
	}
}

func TestP02SingleConnectionPool(t *testing.T) {
	h, _, _, _ := p02Fixture(t, "2026-10-10")
	cfg, err := pgxpool.ParseConfig(DatabaseDSN())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service := reminders.NewService(reminders.Options{Store: reminders.NewRepository(pool), Sender: h.Mail, Clock: h.Clock})
	report, err := service.Dispatch(ctx)
	if err != nil || report.Sent != 2 {
		t.Fatal("operational lock exhausted pool", report, err)
	}
}

func TestP02MailpitDeliveryEvidence(t *testing.T) {
	if !mailpitReachable() {
		t.Skip("P02 SMTP acceptance requires local Mailpit")
	}
	h, _, _, task := p02Fixture(t, "2026-10-10")
	name := fmt.Sprintf("P02-Mailpit-%d", time.Now().UnixNano())
	p02SQL(t, h, task, h.Clock.Now(), `UPDATE tasks SET name=$2 WHERE id=$1`, name)
	sender, err := mail.NewSMTP(mail.SMTPConfig{Host: "127.0.0.1", Port: 11025, FromAddress: "p02@example.test", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	service := reminders.NewService(reminders.Options{Store: reminders.NewRepository(h.Pool), Sender: sender, Clock: h.Clock, BatchSize: 1})
	report, err := service.Dispatch(context.Background())
	if err != nil || report.Sent != 1 {
		t.Fatal(report, err)
	}
	var subject string
	err = h.Pool.QueryRow(context.Background(), `SELECT snapshot->>'Subject' FROM reminder_delivery_attempts WHERE task_id=$1 AND status='sent'`, task).Scan(&subject)
	if err != nil || !strings.Contains(subject, name) || !mailpitHasSubject(t, subject) {
		t.Fatal("SMTP receipt and stored evidence differ", subject, err)
	}
}

func TestP02ProjectCapacityAndAtomicQualification(t *testing.T) {
	h, owner, project, task := p02Fixture(t, "2026-10-13")
	ctx := context.Background()
	// One transaction inserts a representative small-group project. Deferred
	// rollup notifications must not cause an unbounded synchronization cascade.
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT set_config('worklane.reminder_now',$1,true)`, h.Clock.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, project); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `WITH created AS (INSERT INTO tasks(id,project_id,created_by,updated_by,name,start_date,end_date)
 SELECT gen_random_uuid(),$1,$2,$2,'capacity-'||n,'2026-10-01','2026-10-13' FROM generate_series(1,40) n RETURNING id)
 INSERT INTO task_members(id,task_id,user_id,role) SELECT gen_random_uuid(),id,$2,'owner' FROM created`, project, owner.UserID); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(begin)
	if elapsed > 15*time.Second {
		t.Fatalf("41-task project synchronization exceeded acceptance bound: %v", elapsed)
	}
	setReminderEnabled(t, owner, false)
	var n int
	err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders r JOIN tasks t ON t.id=r.task_id WHERE t.project_id=$1 AND r.status='pending'`, project).Scan(&n)
	if err != nil || n != 0 {
		t.Fatal("bulk qualification left sendable plans", task, n, err)
	}
	setReminderEnabled(t, owner, true)
	err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders r JOIN tasks t ON t.id=r.task_id WHERE t.project_id=$1 AND r.kind='task_due_soon' AND r.status='pending'`, project).Scan(&n)
	if err != nil || n != 41 {
		t.Fatal("bulk restore missed plans", n, err)
	}
	t.Logf("41-task deferred commit: %s; qualification cancellation/restoration verified", elapsed)
}

func TestP02FutureTaskNeedsNoFurtherEdits(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "p02future")
	group := h.CreateGroup(t, owner, "future")
	project := h.CreateProject(t, owner, group, "future")
	task := h.CreateTask(t, owner, project, "no further edits", "2026-10-19", "2026-10-22")
	for _, kind := range []string{"task_start_soon", "task_due_soon", "task_overdue"} {
		_, at, state := p02Plan(t, h, task, kind)
		if state != "pending" {
			t.Fatal("future plan absent", kind, state)
		}
		h.Clock.Set(at.Add(time.Minute))
		if _, err := h.Reminders.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, _, state = p02Plan(t, h, task, kind)
		if state != "sent" {
			t.Fatal("clock advancement alone did not deliver", kind, state)
		}
	}
	before := len(h.Mail.Messages())
	h.Clock.Set(h.Clock.Now().Add(24 * time.Hour))
	if _, err := h.Reminders.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.Mail.Messages()) != before {
		t.Fatal("persistent overdue became daily nagging")
	}
}

func TestP02ParentDeletionAndReparentSynchronize(t *testing.T) {
	h, owner, project, parent := p02Fixture(t, "2026-10-20")
	now := h.Clock.Now()
	child := Str(owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": "child", "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-13"}).RequireStatus(t, 201).Data(t), "id")
	p02SQL(t, h, parent, now, `UPDATE tasks SET deleted_at=now() WHERE id=$1`)
	_, _, state := p02Plan(t, h, child, "task_due_soon")
	if state != "pending" {
		t.Fatal("deleting parent cancelled surviving child", state)
	}
	_, _, state = p02Plan(t, h, parent, "task_due_soon")
	if state != "cancelled" {
		t.Fatal("deleted parent kept plan", state)
	}
	p02SQL(t, h, parent, now, `UPDATE tasks SET deleted_at=NULL WHERE id=$1`)
	target := h.CreateTask(t, owner, project, "new parent", "2026-10-01", "2026-10-20")
	p02SQL(t, h, child, now, `UPDATE tasks SET progress=100,status='completed' WHERE id=$1`)
	_, _, state = p02Plan(t, h, parent, "task_due_soon")
	if state == "pending" {
		t.Fatal("derived completion did not cancel parent")
	}
	p02SQL(t, h, child, now, `UPDATE tasks SET parent_id=$2 WHERE id=$1`, target)
	_, _, state = p02Plan(t, h, target, "task_due_soon")
	if state == "pending" {
		t.Fatal("new ancestor completion did not cancel plan")
	}
}

func TestP02AuthorizationCommitFailureDoesNotSend(t *testing.T) {
	h, _, _, _ := p02Fixture(t, "2026-10-10")
	ctx := context.Background()
	_, err := h.Pool.Exec(ctx, `CREATE FUNCTION p02_fail_authorization() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected authorization commit failure'; END $$;
 CREATE CONSTRAINT TRIGGER p02_fail_authorization AFTER INSERT ON reminder_delivery_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p02_fail_authorization()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS p02_fail_authorization ON reminder_delivery_attempts; DROP FUNCTION IF EXISTS p02_fail_authorization()`)
	})
	if _, err = h.Reminders.Dispatch(ctx); err == nil {
		t.Fatal("authorization commit failure hidden")
	}
	if len(h.Mail.Messages()) != 0 {
		t.Fatal("SMTP invoked without confirmed authorization commit")
	}
	var n int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM reminder_delivery_attempts`).Scan(&n)
	if n != 0 {
		t.Fatal("failed commit retained authorization", n)
	}
}
