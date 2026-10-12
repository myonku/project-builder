package testsupport

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/requestid"
)

// 本文件覆盖 Audit 域：端点可见性、筛选与分页、枚举与数据库 CHECK 的一致性，
// 以及"审计与业务同事务"这一最关键的语义。

// writeAudit 用池（自动提交）写一条审计，作为列表用例的前置数据。
func writeAudit(t *testing.T, harness *Harness, entry domain.AuditEntry) string {
	t.Helper()

	before := countAuditRows(t, harness)
	if err := harness.Audit.WriteAudit(context.Background(), harness.Pool, entry); err != nil {
		t.Fatalf("写入操作记录失败: %v", err)
	}
	if after := countAuditRows(t, harness); after != before+1 {
		t.Fatalf("写入后记录数 = %d，期望 %d", after, before+1)
	}

	var id string
	err := harness.Pool.QueryRow(context.Background(),
		`SELECT id FROM activity_logs ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id)
	if err != nil {
		t.Fatalf("读取刚写入的记录 id 失败: %v", err)
	}
	return id
}

// countAuditRows 统计审计表行数。
func countAuditRows(t *testing.T, harness *Harness) int {
	t.Helper()

	var total int
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_logs`).Scan(&total); err != nil {
		t.Fatalf("统计审计行失败: %v", err)
	}
	return total
}

// pinAuditCreatedAt 把一条记录的时间钉死。
//
// 审计的 created_at 由数据库 now() 生成（真实时钟）。断言时间范围时若直接用
// "现在"，结果就取决于运行时刻与机器快慢——红线 9 的同类问题。
// 因此这里显式把时间改成确定值，让范围断言完全可复现。
func pinAuditCreatedAt(t *testing.T, harness *Harness, id string, at time.Time) {
	t.Helper()

	tag, err := harness.Pool.Exec(context.Background(),
		`UPDATE activity_logs SET created_at = $1 WHERE id = $2`, at.UTC(), id)
	if err != nil {
		t.Fatalf("固定记录时间失败: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("固定记录时间影响了 %d 行，期望 1 行", tag.RowsAffected())
	}
}

// TestAuditEnumWhitelistMatchesDatabaseCheck 断言领域白名单与数据库 CHECK 完全一致。
//
// 这是本项目最重要的一致性测试之一（与 domain 的 TestEnumWhitelistsMatchDatabaseConstraints 同源）：
// 领域放行、数据库拒绝会变成 500；数据库放行、领域不认识则会写出无法归类的记录。
func TestAuditEnumWhitelistMatchesDatabaseCheck(t *testing.T) {
	harness := NewHarness(t)

	tests := []struct {
		constraint string
		want       []string
	}{
		{"ck_activity_logs_action", auditStrings(domain.AuditActions())},
		{"ck_activity_logs_object_type", auditStrings(domain.AuditObjectTypes())},
	}

	for _, tc := range tests {
		t.Run(tc.constraint, func(t *testing.T) {
			var definition string
			err := harness.Pool.QueryRow(context.Background(),
				`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`,
				tc.constraint).Scan(&definition)
			if err != nil {
				t.Fatalf("读取约束 %s 失败: %v", tc.constraint, err)
			}

			got := quotedValues(definition)
			assertSameStringSet(t, tc.constraint, got, tc.want)
		})
	}
}

// TestAuditEndpointRequiresAdministrator 覆盖端点的可见性规则。
func TestAuditEndpointRequiresAdministrator(t *testing.T) {
	harness := NewHarness(t)

	member := harness.RegisterAndLogin(t, "audit_member")
	admin := harness.RegisterAndLogin(t, "audit_admin")
	promoteToAdmin(t, harness, admin.UserID)
	// 注册本身也会写审计（写路径已接入），本用例只关心查询层语义，先清空。
	clearAuditRows(t, harness)

	t.Run("未登录返回 401", func(t *testing.T) {
		harness.Client(t).Get(URL("/audit")).
			RequireStatus(t, 401).RequireCode(t, string(domain.CodeAuthenticationReq))
	})

	t.Run("普通成员返回 403", func(t *testing.T) {
		member.Client.Get(URL("/audit")).
			RequireStatus(t, 403).RequireCode(t, string(domain.CodePermissionDenied))
	})

	t.Run("管理员返回 200 且空列表不是 null", func(t *testing.T) {
		response := admin.Client.Get(URL("/audit")).RequireStatus(t, 200)
		if items := response.ListData(t); len(items) != 0 {
			t.Fatalf("初始列表应为空，实际 %d 条", len(items))
		}
		if total := Int(response.Pagination(t), "total"); total != 0 {
			t.Fatalf("pagination.total = %d，期望 0", total)
		}
	})
}

// TestAuditListFiltersAndTimeRange 覆盖筛选与时间范围。
func TestAuditListFiltersAndTimeRange(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLogin(t, "audit_actor")
	other := harness.RegisterAndLogin(t, "audit_other")
	admin := harness.RegisterAndLogin(t, "audit_filter_admin")
	promoteToAdmin(t, harness, admin.UserID)
	// 三条注册记录会污染下面的计数断言，先清空（只保留本用例自己造的数据）。
	clearAuditRows(t, harness)

	base := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	projectID := "01a12d87-0000-7000-8000-00000000a001"
	taskID := "01a12d87-0000-7000-8000-00000000a002"

	created := writeAudit(t, harness, domain.AuditEntry{
		ActorID: actor.UserID, Action: domain.AuditActionCreated,
		ObjectType: domain.AuditObjectProject, ObjectID: projectID,
		Changes: map[string]string{"name": "项目 A"},
	})
	updated := writeAudit(t, harness, domain.AuditEntry{
		ActorID: actor.UserID, Action: domain.AuditActionUpdated,
		ObjectType: domain.AuditObjectTask, ObjectID: taskID,
	})
	archived := writeAudit(t, harness, domain.AuditEntry{
		ActorID: other.UserID, Action: domain.AuditActionArchived,
		ObjectType: domain.AuditObjectProject, ObjectID: projectID,
	})

	// 三条记录时间依次相隔一小时，范围断言才有区分度。
	pinAuditCreatedAt(t, harness, created, base)
	pinAuditCreatedAt(t, harness, updated, base.Add(time.Hour))
	pinAuditCreatedAt(t, harness, archived, base.Add(2*time.Hour))

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"按对象类型", "object_type=project", 2},
		{"按动作", "action=updated", 1},
		{"按对象 id", "object_id=" + taskID, 1},
		{"按操作人", "actor_id=" + other.UserID, 1},
		{"组合筛选", "object_type=project&action=archived", 1},
		{"时间下界（含边界）", "from=" + base.Add(time.Hour).Format(time.RFC3339), 2},
		{"时间上界（含边界）", "to=" + base.Add(time.Hour).Format(time.RFC3339), 2},
		{"时间范围收窄", "from=" + base.Format(time.RFC3339) + "&to=" + base.Add(time.Hour).Format(time.RFC3339), 2},
		{"范围之外为空", "from=" + base.Add(3*time.Hour).Format(time.RFC3339), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := admin.Client.Get(URL("/audit?"+tc.query)).RequireStatus(t, 200)
			if got := Int(response.Pagination(t), "total"); got != tc.want {
				t.Fatalf("total = %d，期望 %d（查询：%s）", got, tc.want, tc.query)
			}
			if got := len(response.ListData(t)); got != tc.want {
				t.Fatalf("条目数 = %d，期望 %d（查询：%s）", got, tc.want, tc.query)
			}
		})
	}

	t.Run("changes 原样返回", func(t *testing.T) {
		response := admin.Client.Get(URL("/audit?action=created")).RequireStatus(t, 200)
		items := response.ListData(t)
		if len(items) != 1 {
			t.Fatalf("条目数 = %d，期望 1", len(items))
		}
		changes, ok := items[0]["changes"].(map[string]any)
		if !ok {
			t.Fatalf("changes 不是对象: %#v", items[0]["changes"])
		}
		if changes["name"] != "项目 A" {
			t.Fatalf("changes.name = %v，期望 项目 A", changes["name"])
		}
		if items[0]["actor_name"] != actor.Username {
			t.Fatalf("actor_name = %v，期望 %s", items[0]["actor_name"], actor.Username)
		}
	})
}

// TestAuditListPaginationAndValidation 覆盖分页、排序与参数校验。
func TestAuditListPaginationAndValidation(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "audit_page_admin")
	promoteToAdmin(t, harness, admin.UserID)
	// 注册记录会混进分页断言，先清空。
	clearAuditRows(t, harness)

	base := time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)
	const total = 7
	for i := 0; i < total; i++ {
		id := writeAudit(t, harness, domain.AuditEntry{
			ActorID: admin.UserID, Action: domain.AuditActionUpdated,
			ObjectType: domain.AuditObjectTask,
			ObjectID:   fmt.Sprintf("01a12d87-0000-7000-8000-0000000b%04d", i),
		})
		pinAuditCreatedAt(t, harness, id, base.Add(time.Duration(i)*time.Minute))
	}

	t.Run("翻页不重不漏", func(t *testing.T) {
		seen := map[string]int{}
		for page := 1; page <= 4; page++ {
			response := admin.Client.
				Get(fmt.Sprintf("%s?page=%d&page_size=2&sort=created_at", URL("/audit"), page)).
				RequireStatus(t, 200)
			for _, item := range response.ListData(t) {
				seen[Str(item, "id")]++
			}
		}
		if len(seen) != total {
			t.Fatalf("翻页共见到 %d 条，期望 %d 条", len(seen), total)
		}
		for id, times := range seen {
			if times != 1 {
				t.Fatalf("记录 %s 出现 %d 次，期望 1 次", id, times)
			}
		}
	})

	t.Run("默认按时间倒序", func(t *testing.T) {
		response := admin.Client.Get(URL("/audit?page_size=1")).RequireStatus(t, 200)
		items := response.ListData(t)
		if len(items) != 1 {
			t.Fatalf("条目数 = %d，期望 1", len(items))
		}
		// 最后写入的那条时间最晚，因此排在最前。
		if got := Str(items[0], "created_at"); got != domain.FormatTimestamp(base.Add(6*time.Minute)) {
			t.Fatalf("首条 created_at = %s，期望 %s", got, domain.FormatTimestamp(base.Add(6*time.Minute)))
		}
	})

	invalid := []struct {
		name  string
		query string
		code  domain.ErrorCode
		field string
	}{
		{"未知查询参数", "kinds=updated", domain.CodeInvalidRequest, "query"},
		{"未知对象类型", "object_type=unknown", domain.CodeInvalidRequest, "object_type"},
		{"未知动作", "action=unknown", domain.CodeInvalidRequest, "action"},
		{"非法 object_id", "object_id=not-a-uuid", domain.CodeInvalidRequest, "object_id"},
		{"非法 actor_id", "actor_id=not-a-uuid", domain.CodeInvalidRequest, "actor_id"},
		{"非法时间格式", "from=2026-10-12", domain.CodeInvalidRequest, "from"},
		{"时间范围倒置", "from=2026-10-13T00:00:00Z&to=2026-10-12T00:00:00Z", domain.CodeInvalidDateRange, "to"},
		{"page_size 越界", "page_size=500", domain.CodeInvalidRequest, "page_size"},
		{"page 越界", "page=0", domain.CodeInvalidRequest, "page"},
		{"未知排序字段", "sort=dedupe_key", domain.CodeInvalidRequest, "sort"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			admin.Client.Get(URL("/audit?"+tc.query)).
				RequireStatus(t, 400).RequireCode(t, string(tc.code)).RequireField(t, tc.field)
		})
	}
}

// TestAuditRedactsSensitiveChangesInDatabase 证明脱敏发生在**入库之前**。
//
// 只断言响应不泄漏是不够的：真正危险的是"已经写进一张只增不删的表"。
func TestAuditRedactsSensitiveChangesInDatabase(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "audit_redact_admin")
	promoteToAdmin(t, harness, admin.UserID)

	id := writeAudit(t, harness, domain.AuditEntry{
		ActorID: admin.UserID, Action: domain.AuditActionPasswordReset,
		ObjectType: domain.AuditObjectUser, ObjectID: admin.UserID,
		Changes: map[string]string{
			"password":     "super-secret",
			"display_name": "张三",
		},
	})

	var changes string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT changes::text FROM activity_logs WHERE id = $1`, id).Scan(&changes); err != nil {
		t.Fatalf("读取 changes 失败: %v", err)
	}
	if strings.Contains(changes, "super-secret") {
		t.Fatalf("数据库里出现了明文密码: %s", changes)
	}
	if !strings.Contains(changes, "[redacted]") {
		t.Fatalf("敏感字段未被标记为 [redacted]: %s", changes)
	}
	if !strings.Contains(changes, "张三") {
		t.Fatalf("非敏感字段被误删: %s", changes)
	}
}

// TestAuditRequestIDComesFromContext 覆盖 request id 的来源。
func TestAuditRequestIDComesFromContext(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLogin(t, "audit_reqid")
	ctx := requestid.With(context.Background(), "req-from-context")

	if err := harness.Audit.WriteAudit(ctx, harness.Pool, domain.AuditEntry{
		ActorID: actor.UserID, Action: domain.AuditActionCreated,
		ObjectType: domain.AuditObjectGroup,
		ObjectID:   "01a12d87-0000-7000-8000-0000000c0001",
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	var requestID string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT request_id FROM activity_logs ORDER BY created_at DESC LIMIT 1`).Scan(&requestID); err != nil {
		t.Fatalf("读取 request_id 失败: %v", err)
	}
	if requestID != "req-from-context" {
		t.Fatalf("request_id = %q，期望 req-from-context", requestID)
	}
}

// TestAuditSharesTransactionWithBusiness 是本模块最关键的一条：
// 审计行与业务写在同一事务里，业务回滚时审计也必须消失。
//
// 这里用 InTx + Flush 直接验证机制本身（各业务域的写路径由各自的用例覆盖）。
func TestAuditSharesTransactionWithBusiness(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLogin(t, "audit_tx")

	entry := domain.AuditEntry{
		ActorID: actor.UserID, Action: domain.AuditActionUpdated,
		ObjectType: domain.AuditObjectProject,
		ObjectID:   "01a12d87-0000-7000-8000-0000000d0001",
	}

	t.Run("提交后记录存在", func(t *testing.T) {
		before := countAuditRows(t, harness)
		err := apperr.InTx(context.Background(), harness.Pool, func(ctx context.Context, tx apperr.Querier) error {
			return apperr.NewAuditWrite(harness.Audit, entry).Flush(ctx, tx)
		})
		if err != nil {
			t.Fatalf("事务提交失败: %v", err)
		}
		if got := countAuditRows(t, harness); got != before+1 {
			t.Fatalf("提交后记录数 = %d，期望 %d", got, before+1)
		}
	})

	t.Run("回滚后记录不存在", func(t *testing.T) {
		before := countAuditRows(t, harness)
		rollbackErr := fmt.Errorf("业务失败")
		err := apperr.InTx(context.Background(), harness.Pool, func(ctx context.Context, tx apperr.Querier) error {
			if err := apperr.NewAuditWrite(harness.Audit, entry).Flush(ctx, tx); err != nil {
				return err
			}
			return rollbackErr
		})
		if err != rollbackErr {
			t.Fatalf("事务错误 = %v，期望业务错误原样返回", err)
		}
		if got := countAuditRows(t, harness); got != before {
			t.Fatalf("回滚后记录数 = %d，期望 %d（审计必须随业务一起回滚）", got, before)
		}
	})

	t.Run("未注入写入方时是空操作", func(t *testing.T) {
		before := countAuditRows(t, harness)
		err := apperr.InTx(context.Background(), harness.Pool, func(ctx context.Context, tx apperr.Querier) error {
			// harness 为 nil 的 writer：模块在单元测试里不注入审计时的形态。
			return apperr.NewAuditWrite(nil, entry).Flush(ctx, tx)
		})
		if err != nil {
			t.Fatalf("空操作返回错误: %v", err)
		}
		if got := countAuditRows(t, harness); got != before {
			t.Fatalf("记录数 = %d，期望 %d", got, before)
		}
	})
}

// TestAuditRejectsIncompleteEntry 覆盖落库前的校验（写入方给出坏数据时不落库）。
func TestAuditRejectsIncompleteEntry(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLogin(t, "audit_invalid")

	tests := []struct {
		name  string
		entry domain.AuditEntry
	}{
		{"对象类型未知", domain.AuditEntry{
			ActorID: actor.UserID, Action: domain.AuditActionCreated,
			ObjectType: "unknown", ObjectID: "01a12d87-0000-7000-8000-0000000e0001",
		}},
		{"动作未知", domain.AuditEntry{
			ActorID: actor.UserID, Action: "unknown",
			ObjectType: domain.AuditObjectTask, ObjectID: "01a12d87-0000-7000-8000-0000000e0002",
		}},
		{"对象 id 不是 UUID", domain.AuditEntry{
			ActorID: actor.UserID, Action: domain.AuditActionCreated,
			ObjectType: domain.AuditObjectTask, ObjectID: "not-a-uuid",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := countAuditRows(t, harness)
			if err := harness.Audit.WriteAudit(context.Background(), harness.Pool, tc.entry); err == nil {
				t.Fatal("非法记录未被拒绝")
			}
			if got := countAuditRows(t, harness); got != before {
				t.Fatalf("非法记录被写入：记录数 = %d，期望 %d", got, before)
			}
		})
	}
}

// auditStrings 把审计枚举转为字符串切片。
func auditStrings[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

// quotedValues 从 CHECK 定义里提取全部单引号字面量。
//
// pg_get_constraintdef 返回形如：
//
//	CHECK (((action)::text = ANY ((ARRAY['created'::character varying, ...])::text[])))
func quotedValues(definition string) []string {
	var values []string
	for i := 0; i < len(definition); i++ {
		if definition[i] != '\'' {
			continue
		}
		end := i + 1
		for end < len(definition) && definition[end] != '\'' {
			end++
		}
		values = append(values, definition[i+1:end])
		i = end
	}
	return values
}

// assertSameStringSet 断言两个集合完全相等（去重后）。
func assertSameStringSet(t *testing.T, name string, got, want []string) {
	t.Helper()

	gotSet := map[string]bool{}
	for _, v := range got {
		gotSet[v] = true
	}
	wantSet := map[string]bool{}
	for _, v := range want {
		wantSet[v] = true
	}

	for v := range wantSet {
		if !gotSet[v] {
			t.Errorf("%s 缺少取值 %q（实际：%v）", name, v, got)
		}
	}
	for v := range gotSet {
		if !wantSet[v] {
			t.Errorf("%s 存在多余取值 %q（期望：%v）", name, v, want)
		}
	}
	if len(gotSet) != len(wantSet) {
		t.Errorf("%s 去重后大小 = %d，期望 %d", name, len(gotSet), len(wantSet))
	}
}
