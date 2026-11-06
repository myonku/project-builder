package testsupport

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/app/groups"
	"github.com/myonku/project-builder/internal/database"
	"github.com/myonku/project-builder/internal/domain"
)

func submitGroupCreation(t *testing.T, a Authenticated, name, key string) Response {
	t.Helper()
	return a.Client.Call("POST", URL("/group-creation-requests"), map[string]any{"name": name, "description": "申请说明", "reason": "需要组织项目协作"}, map[string]string{"Idempotency-Key": key})
}
func reviewGroupCreation(t *testing.T, admin Authenticated, request map[string]any, status string) Response {
	t.Helper()
	body := map[string]any{"status": status, "expected_updated_at": request["updated_at"]}
	if status == "rejected" {
		body["rejection_reason"] = "请补充工作范围后重新申请"
	}
	return admin.Client.Post(URL("/group-creation-requests/"+Str(request, "id")+"/review"), body)
}

func TestGroupCreationLifecycleAndPermissions(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	a := h.RegisterAndLogin(t, "creation_a")
	b := h.RegisterAndLogin(t, "creation_b")
	admin := h.RegisterAndLogin(t, "creation_admin")
	promoteToAdmin(t, h, admin.UserID)
	h.CreateGroup(t, b, "已有组管理员")
	t.Run("direct creation is admin only including service", func(t *testing.T) {
		a.Client.Post(URL("/groups"), map[string]any{"name": "绕过审核"}).RequireStatus(t, 403)
		_, err := h.Groups.Create(ctx, domain.User{ID: a.UserID, Status: "active", SystemRole: "member"}, groups.CreateInput{Name: "绕过服务"})
		if err == nil {
			t.Fatal("service allowed direct member creation")
		}
		_, err = h.Groups.Create(ctx, domain.User{ID: a.UserID, Status: "active", SystemRole: "admin"}, groups.CreateInput{Name: "过期管理员"})
		if err == nil {
			t.Fatal("repository trusted stale administrator role")
		}
		admin.Client.Post(URL("/groups"), map[string]any{"name": "管理员直接组"}).RequireStatus(t, 201)
	})
	h.Client(t).Post(URL("/group-creation-requests"), map[string]any{}).RequireStatus(t, 401)
	request := submitGroupCreation(t, a, " 申请研发组 ", "lifecycle").RequireStatus(t, 201).Data(t)
	if request["group_id"] != nil || request["status"] != "pending" || request["name"] != "申请研发组" {
		t.Fatalf("bad pending projection: %v", request)
	}
	if _, ok := request["applicant_id"]; ok {
		t.Fatal("my response leaked admin fields")
	}
	id := Str(request, "id")
	b.Client.Get(URL("/group-creation-requests/"+id)).RequireStatus(t, 404)
	b.Client.Post(URL("/group-creation-requests/query"), map[string]any{}).RequireStatus(t, 403)
	reviewGroupCreation(t, b, request, "approved").RequireStatus(t, 403)
	a.Client.Post(URL("/me/group-creation-requests/query"), map[string]any{"applicant_id": b.UserID}).RequireStatus(t, 422)
	if len(b.Client.Post(URL("/me/group-creation-requests/query"), map[string]any{}).RequireStatus(t, 200).ListData(t)) != 0 {
		t.Fatal("my list leaked another applicant")
	}
	submitGroupCreation(t, b, "申请研发组", "lifecycle").RequireStatus(t, 201)
	submitGroupCreation(t, a, "申请研发组", "different-key").RequireStatus(t, 409)
	submitGroupCreation(t, a, "申请研发组", "lifecycle").RequireStatus(t, 200)
	approved := reviewGroupCreation(t, admin, request, "approved").RequireStatus(t, 200).Data(t)
	groupID := Str(approved, "group_id")
	if groupID == "" || approved["reviewed_by"] != admin.UserID {
		t.Fatal(approved)
	}
	group := a.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, 200).Data(t)
	if group["owner_id"] != a.UserID || group["membership_role"] != "manager" {
		t.Fatal(group)
	}
	var reviewers int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, admin.UserID).Scan(&reviewers); err != nil || reviewers != 0 {
		t.Fatalf("reviewer membership: %d %v", reviewers, err)
	}
	requireAuditRow(t, auditRowsFor(t, h, groupID), "created", a.UserID, id)
	requireAuditRow(t, auditRowsFor(t, h, id), "group_creation_reviewed", "approved", a.UserID)
	reviewGroupCreation(t, admin, request, "approved").RequireStatus(t, 409)
	replay := submitGroupCreation(t, a, "申请研发组", "lifecycle").RequireStatus(t, 200).Data(t)
	if replay["group_id"] != groupID || replay["status"] != "approved" {
		t.Fatal(replay)
	}
	// A later same-name group is allowed; only pending duplicates per applicant are forbidden.
	rejectedRequest := submitGroupCreation(t, a, "申请研发组", "second-round").RequireStatus(t, 201).Data(t)
	rejected := reviewGroupCreation(t, admin, rejectedRequest, "rejected").RequireStatus(t, 200).Data(t)
	if rejected["rejection_reason"] == nil || rejected["group_id"] != nil {
		t.Fatal(rejected)
	}
	submitGroupCreation(t, a, "申请研发组", "third-round").RequireStatus(t, 201)
	if got := len(a.Client.Post(URL("/me/group-creation-requests/query"), map[string]any{}).RequireStatus(t, 200).ListData(t)); got != 3 {
		t.Fatalf("history count %d", got)
	}
	// Historical approval must not expose current group details after access is removed.
	h.AddGroupMember(t, a, groupID, b.UserID)
	a.Client.Patch(URL("/groups/"+groupID), map[string]any{"owner_id": b.UserID, "expected_updated_at": group["updated_at"]}).RequireStatus(t, 200)
	b.Client.Delete(URL("/groups/"+groupID+"/members/"+a.UserID), nil).RequireStatus(t, 204)
	historical := a.Client.Get(URL("/group-creation-requests/"+id)).RequireStatus(t, 200).Data(t)
	if historical["status"] != "approved" || historical["group_name"] != nil || historical["can_access_group"] != false {
		t.Fatal(historical)
	}
}

func TestGroupCreationValidationAndInactiveAccounts(t *testing.T) {
	h := NewHarness(t)
	a := h.RegisterAndLogin(t, "creation_v")
	admin := h.RegisterAndLogin(t, "creation_va")
	promoteToAdmin(t, h, admin.UserID)
	for _, body := range []map[string]any{
		{"name": "x", "reason": " "}, {"name": " ", "reason": "用途"}, {"name": "x", "reason": "用途", "owner_id": a.UserID},
	} {
		a.Client.Call("POST", URL("/group-creation-requests"), body, map[string]string{"Idempotency-Key": "invalid"}).RequireStatus(t, 422)
	}
	a.Client.Post(URL("/group-creation-requests"), map[string]any{"name": "x", "reason": "用途"}).RequireStatus(t, 422)
	rec := submitGroupCreation(t, a, "版本检查", "v").RequireStatus(t, 201).Data(t)
	path := URL("/group-creation-requests/" + Str(rec, "id") + "/review")
	for _, body := range []map[string]any{
		{"status": "approved"}, {"status": "rejected", "expected_updated_at": rec["updated_at"]},
		{"status": "pending", "expected_updated_at": rec["updated_at"]},
		{"status": "approved", "rejection_reason": "不应出现", "expected_updated_at": rec["updated_at"]},
	} {
		admin.Client.Post(path, body).RequireStatus(t, 422)
	}
	admin.Client.Post(path, map[string]any{"status": "approved", "expected_updated_at": "2020-01-01T00:00:00Z"}).RequireStatus(t, 409).RequireCode(t, "version_conflict")
	if _, err := h.Pool.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE id=$1`, a.UserID); err != nil {
		t.Fatal(err)
	}
	reviewGroupCreation(t, admin, rec, "approved").RequireStatus(t, 409)
	reviewGroupCreation(t, admin, rec, "rejected").RequireStatus(t, 200)
	a.Client.Post(URL("/group-creation-requests"), map[string]any{"name": "x", "reason": "用途"}).RequireStatus(t, 401)
	admin.Client.Get(URL("/group-creation-requests/not-uuid")).RequireStatus(t, 400)
	admin.Client.Post(URL("/group-creation-requests/query"), map[string]any{"applicant_id": "bad"}).RequireStatus(t, 400)
	admin.Client.Post(URL("/group-creation-requests/query"), map[string]any{"sort": "name"}).RequireStatus(t, 400)
	admin.Client.Post(URL("/group-creation-requests/query"), map[string]any{"status": "invalid"}).RequireStatus(t, 400)
}

func TestGroupCreationConcurrentSubmissionAndReview(t *testing.T) {
	h := NewHarness(t)
	a := h.RegisterAndLogin(t, "creation_c")
	admin := h.RegisterAndLogin(t, "creation_ca")
	promoteToAdmin(t, h, admin.UserID)
	secondAdmin := h.RegisterAndLogin(t, "creation_cb")
	promoteToAdmin(t, h, secondAdmin.UserID)
	var wg sync.WaitGroup
	responses := make(chan Response, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- a.Client.Call("POST", URL("/group-creation-requests"), map[string]any{"name": "并发组", "reason": "协作"}, map[string]string{"Idempotency-Key": "concurrent"})
		}()
	}
	wg.Wait()
	close(responses)
	var rec map[string]any
	created := 0
	for resp := range responses {
		if resp.Status == 201 {
			created++
		} else {
			resp.RequireStatus(t, 200)
		}
		data := resp.Data(t)
		if rec != nil && data["id"] != rec["id"] {
			t.Fatal("different replay IDs")
		}
		rec = data
	}
	if created != 1 {
		t.Fatalf("created %d", created)
	}
	outcomes := make(chan Response, 2)
	for _, status := range []string{"approved", "rejected"} {
		wg.Add(1)
		go func(status string) {
			reviewer := admin
			if status == "rejected" {
				reviewer = secondAdmin
			}
			defer wg.Done()
			outcomes <- reviewer.Client.Post(URL("/group-creation-requests/"+Str(rec, "id")+"/review"), map[string]any{"status": status, "expected_updated_at": rec["updated_at"], "rejection_reason": map[string]any{"approved": nil, "rejected": "重复用途"}[status]})
		}(status)
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for response := range outcomes {
		if response.Status == 200 {
			success++
		} else {
			response.RequireStatus(t, 409)
		}
	}
	if success != 1 {
		t.Fatalf("review successes %d", success)
	}
	var count int
	if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM groups WHERE name='并发组'`).Scan(&count); err != nil || count > 1 {
		t.Fatalf("duplicate groups %d %v", count, err)
	}
}

func TestGroupCreationRollbackAndPagination(t *testing.T) {
	h := NewHarness(t)
	a := h.RegisterAndLogin(t, "creation_r")
	admin := h.RegisterAndLogin(t, "creation_ra")
	promoteToAdmin(t, h, admin.UserID)
	ctx := context.Background()
	rec := submitGroupCreation(t, a, "含%_\\字面组", "rollback").RequireStatus(t, 201).Data(t)
	// Inject failure after group creation, when writing the decision audit.
	_, err := h.Pool.Exec(ctx, `CREATE FUNCTION reject_creation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='group_creation_reviewed' THEN RAISE EXCEPTION 'P05 injected audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_creation_audit BEFORE INSERT ON activity_logs FOR EACH ROW EXECUTE FUNCTION reject_creation_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_creation_audit ON activity_logs; DROP FUNCTION IF EXISTS reject_creation_audit()`)
	reviewGroupCreation(t, admin, rec, "approved").RequireStatus(t, 500)
	var count int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM groups WHERE name=$1`, rec["name"]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan groups %d %v", count, err)
	}
	state := a.Client.Get(URL("/group-creation-requests/"+Str(rec, "id"))).RequireStatus(t, 200).Data(t)
	if state["status"] != "pending" || countAuditRowsFor(t, h, Str(rec, "id")) != 1 {
		t.Fatal(state)
	}
	if _, err := h.Pool.Exec(ctx, `DROP TRIGGER reject_creation_audit ON activity_logs; DROP FUNCTION reject_creation_audit()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		submitGroupCreation(t, a, fmt.Sprintf("分页%d", i), fmt.Sprint(i)).RequireStatus(t, 201)
	}
	matches := admin.Client.Post(URL("/group-creation-requests/query"), map[string]any{"q": "%_\\"}).RequireStatus(t, 200).ListData(t)
	if len(matches) != 1 {
		t.Fatal(matches)
	}
	page := admin.Client.Post(URL("/group-creation-requests/query"), map[string]any{"page_size": 2, "sort": "created_at"}).RequireStatus(t, 200)
	if len(page.ListData(t)) != 2 || page.Decode(t)["pagination"].(map[string]any)["total"] != float64(4) {
		t.Fatal(string(page.Body))
	}
	// Pin equal primary sort values to exercise the stable id tiebreaker across pages.
	if _, err := h.Pool.Exec(ctx, `UPDATE group_creation_requests SET created_at='2026-11-06T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for number := 1; number <= 4; number++ {
		rows := a.Client.Post(URL("/me/group-creation-requests/query"), map[string]any{"page": number, "page_size": 1, "sort": "created_at"}).RequireStatus(t, 200).ListData(t)
		if len(rows) != 1 || seen[Str(rows[0], "id")] {
			t.Fatalf("unstable pagination: %v", rows)
		}
		seen[Str(rows[0], "id")] = true
	}

}

func TestGroupCreationDatabaseConstraintsAndMigration(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	a := h.RegisterAndLogin(t, "creation_db")
	admin := h.RegisterAndLogin(t, "creation_dba")
	promoteToAdmin(t, h, admin.UserID)
	historical := h.CreateGroup(t, a, "迁移前组")
	m, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Up(); m.Close() }()
	if err := migrateToVersion(m, 12); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err := h.Pool.QueryRow(ctx, `SELECT to_regclass('group_creation_requests') IS NULL`).Scan(&missing); err != nil || !missing {
		t.Fatalf("head12 %v %v", missing, err)
	}
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
	approved := reviewGroupCreation(t, admin, submitGroupCreation(t, a, "迁移批准组", "db").RequireStatus(t, 201).Data(t), "approved").RequireStatus(t, 200).Data(t)
	groupID := Str(approved, "group_id")
	for _, values := range []struct{ name, reason, status string }{
		{" ", "用途", "pending"}, {"合法", " ", "pending"}, {"合法", "用途", "invalid"}, {"合法", "用途", "approved"}, {"合法", "用途", "rejected"},
	} {
		if _, err := h.Pool.Exec(ctx, `INSERT INTO group_creation_requests(id,applicant_id,name,reason,status) VALUES(gen_random_uuid(),$1,$2,$3,$4)`, a.UserID, values.name, values.reason, values.status); err == nil {
			t.Fatalf("invalid state accepted: %+v", values)
		}
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO group_creation_requests(id,applicant_id,name,reason,status,reviewed_by,reviewed_at,group_id) VALUES(gen_random_uuid(),$1,'重复组映射','用途','approved',$2,now(),$3)`, a.UserID, admin.UserID, groupID); err == nil {
		t.Fatal("group_id must be unique")
	}
	submitGroupCreation(t, a, "CaseName", "case1").RequireStatus(t, 201)
	submitGroupCreation(t, a, "casename", "case2").RequireStatus(t, 409)
	unicode := strings.Repeat("😀", 160)
	submitGroupCreation(t, a, unicode, "unicode").RequireStatus(t, 201)
	submitGroupCreation(t, a, unicode+"😀", "unicode-over").RequireStatus(t, 422)
	// Export workflow evidence before destructive down, as required by the operational plan.
	var exported []byte
	if err := h.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(r)) FROM group_creation_requests r`).Scan(&exported); err != nil || len(exported) == 0 {
		t.Fatalf("export %v", err)
	}
	if err := migrateToVersion(m, 12); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM groups g JOIN group_members m ON m.group_id=g.id AND m.user_id=g.owner_id AND m.role='manager' AND m.status='active' WHERE g.id IN ($1,$2)`, historical, groupID).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("rollback lost group memberships %d %v", retained, err)
	}
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM group_creation_requests`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("down/up history semantics %d %v", retained, err)
	}
}

func TestGroupCreationSubmitAuditRollback(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	a := h.RegisterAndLogin(t, "creation_submit_fail")
	_, err := h.Pool.Exec(ctx, `CREATE FUNCTION reject_creation_submit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.object_type='group_creation_request' THEN RAISE EXCEPTION 'P05 submit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_creation_submit BEFORE INSERT ON activity_logs FOR EACH ROW EXECUTE FUNCTION reject_creation_submit()`)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_creation_submit ON activity_logs;DROP FUNCTION IF EXISTS reject_creation_submit()`)
	submitGroupCreation(t, a, "失败提交", "submit-fail").RequireStatus(t, 500)
	var count int
	if err := h.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM group_creation_requests)+(SELECT count(*) FROM idempotency_keys WHERE scope='group_creation_requests.create')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan workflow records %d %v", count, err)
	}
	if _, err := h.Pool.Exec(ctx, `DROP TRIGGER reject_creation_submit ON activity_logs;DROP FUNCTION reject_creation_submit()`); err != nil {
		t.Fatal(err)
	}
	submitGroupCreation(t, a, "失败提交", "submit-fail").RequireStatus(t, 201)
}

func TestGroupCreationAuthorizationLocks(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	a := h.RegisterAndLogin(t, "creation_lock")
	admin := h.RegisterAndLogin(t, "creation_lock_admin")
	backup := h.RegisterAndLogin(t, "creation_lock_backup")
	promoteToAdmin(t, h, admin.UserID)
	promoteToAdmin(t, h, backup.UserID)
	waitForLock := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var blocked bool
			err := h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FOR SHARE%')`).Scan(&blocked)
			if err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("approval did not acquire authorization lock")
	}
	for _, scenario := range []struct {
		name, user, sql string
		code            int
	}{
		{"reviewer role revoked", admin.UserID, `UPDATE users SET system_role='member' WHERE id=$1`, 403},
		{"applicant disabled", a.UserID, `UPDATE users SET status='disabled' WHERE id=$1`, 409},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rec := submitGroupCreation(t, a, scenario.name, scenario.name).RequireStatus(t, 201).Data(t)
			tx, err := h.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, scenario.sql, scenario.user); err != nil {
				t.Fatal(err)
			}
			done := make(chan Response, 1)
			go func() {
				done <- admin.Client.Post(URL("/group-creation-requests/"+Str(rec, "id")+"/review"), map[string]any{"status": "approved", "expected_updated_at": rec["updated_at"]})
			}()
			waitForLock()
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-done:
				response.RequireStatus(t, scenario.code)
			case <-time.After(5 * time.Second):
				t.Fatal("review deadlocked")
			}
			if _, err := h.Pool.Exec(ctx, `UPDATE users SET system_role='admin' WHERE id=$1`, admin.UserID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
