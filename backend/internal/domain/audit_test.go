package domain

import (
	"strings"
	"testing"
)

// TestAuditWhitelistsMatchContract 锁定两个枚举白名单的取值集合。
//
// 取值必须与迁移 000005 的 CHECK 约束、《A09》 的表格逐条一致。
// 数据库一侧由 testsupport 的 TestAuditEnumWhitelistMatchesDatabaseCheck 在真实库上断言。
func TestAuditWhitelistsMatchContract(t *testing.T) {
	t.Parallel()

	objectTypes := make([]string, 0, len(auditObjectTypes))
	for _, item := range AuditObjectTypes() {
		objectTypes = append(objectTypes, string(item))
	}
	assertSameSet(t, "AuditObjectTypes", objectTypes,
		"user", "group", "project", "task", "attachment", "reminder")

	actions := make([]string, 0, len(auditActions))
	for _, item := range AuditActions() {
		actions = append(actions, string(item))
	}
	assertSameSet(t, "AuditActions", actions,
		"created", "updated", "archived", "deleted", "purged",
		"member_added", "member_role_changed", "member_removed",
		"owner_transferred", "join_request_reviewed", "shared",
		"password_reset", "disabled")
}

// TestAuditWhitelistAccessorsReturnCopy 保证调用方改不动白名单。
func TestAuditWhitelistAccessorsReturnCopy(t *testing.T) {
	t.Parallel()

	types := AuditObjectTypes()
	types[0] = AuditObjectType("tampered")
	if !IsAuditObjectType("user") {
		t.Fatal("AuditObjectTypes 返回的切片被修改后影响到了内部白名单")
	}

	actions := AuditActions()
	actions[0] = AuditAction("tampered")
	if !IsAuditAction("created") {
		t.Fatal("AuditActions 返回的切片被修改后影响到了内部白名单")
	}
}

// TestSanitizeAuditChangesRedactsSensitiveKeys 覆盖脱敏。
//
// 这是审计功能里最危险的一处：把邮件正文、令牌或密码摘要写进一张
// **只增不删**的表，等于把它们永久留在库里。
func TestSanitizeAuditChangesRedactsSensitiveKeys(t *testing.T) {
	t.Parallel()

	sanitized := SanitizeAuditChanges(map[string]string{
		"display_name":          "张三",
		"password":              "super-secret",
		"user_password":         "super-secret",
		"oauth2_access_token":   "ya29.xxx",
		"password_hash":         "$argon2id$v=19$...",
		"mail_body":             "完整邮件正文",
		"file_content":          "文件内容",
		"session_cookie":        "pb_session=abc",
		"authorization_header":  "Bearer abc",
		"task_description_text": "描述文本",
	})

	for key, value := range sanitized {
		lower := strings.ToLower(key)
		switch {
		case strings.Contains(lower, "display_name"),
			strings.Contains(lower, "description"):
			if value == auditRedactedValue {
				t.Errorf("键 %q 不是敏感字段，却被脱敏", key)
			}
		default:
			if value != auditRedactedValue {
				t.Errorf("键 %q 命中敏感特征，值未被脱敏：%q", key, value)
			}
		}
	}
}

// TestSanitizeAuditChangesTruncatesValues 覆盖值长度上限（按 rune 截断）。
func TestSanitizeAuditChangesTruncatesValues(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("汉", AuditMaxValueLength+50)
	sanitized := SanitizeAuditChanges(map[string]string{"name": long})

	got := sanitized["name"]
	if !strings.HasSuffix(got, auditTruncatedSuffix) {
		t.Fatalf("超长值未被截断：%q", got)
	}
	if runes := []rune(got); len(runes) != AuditMaxValueLength+1 {
		t.Fatalf("截断后长度 = %d，期望 %d（含省略号）", len(runes), AuditMaxValueLength+1)
	}
}

// TestSanitizeAuditChangesCapsKeyCountDeterministically 覆盖键数量上限的稳定性。
//
// map 遍历顺序在 Go 里是随机的：不排序就直接截断，会出现"同一份变更每次记的键不同"，
// 那种不确定性在排查问题时比少记几个键更糟。
func TestSanitizeAuditChangesCapsKeyCountDeterministically(t *testing.T) {
	t.Parallel()

	changes := map[string]string{}
	for _, key := range []string{"k09", "k01", "k05", "k03", "k07", "k02", "k04", "k06", "k08"} {
		changes[key] = "v"
	}

	first := SanitizeAuditChanges(changes)
	for i := 0; i < 20; i++ {
		if got := SanitizeAuditChanges(changes); len(got) != len(first) {
			t.Fatalf("第 %d 次归一化的键数量 = %d，期望 %d", i, len(got), len(first))
		}
	}

	if len(first) != 9 {
		t.Fatalf("键数量 = %d，期望 9（未超过上限时全部保留）", len(first))
	}
	if _, ok := first["k01"]; !ok {
		t.Fatal("字典序最小的键应被保留")
	}
}

// TestSanitizeAuditChangesAlwaysReturnsNonNilMap 保证写库时是 {} 而不是 NULL。
func TestSanitizeAuditChangesAlwaysReturnsNonNilMap(t *testing.T) {
	t.Parallel()

	if got := SanitizeAuditChanges(nil); got == nil || len(got) != 0 {
		t.Fatalf("空输入应返回非 nil 空 map，实际为 %#v", got)
	}
	if got := SanitizeAuditChanges(map[string]string{"  ": "值", "": "值"}); len(got) != 0 {
		t.Fatalf("空键应被丢弃，实际为 %#v", got)
	}
}

// TestValidateAuditEntry 覆盖落库前的校验。
func TestValidateAuditEntry(t *testing.T) {
	t.Parallel()

	valid := AuditEntry{
		ActorID: "01a12d87-0000-7000-8000-000000000001", Action: AuditActionUpdated,
		ObjectType: AuditObjectProject, ObjectID: "01a12d87-0000-7000-8000-000000000002",
	}
	if err := ValidateAuditEntry(valid); err != nil {
		t.Fatalf("合法记录被拒绝: %v", err)
	}

	tests := []struct {
		name  string
		entry AuditEntry
		field string
	}{
		{"缺少操作人", AuditEntry{Action: AuditActionUpdated, ObjectType: AuditObjectTask, ObjectID: "x"}, "actor_id"},
		{"缺少对象", AuditEntry{ActorID: "x", Action: AuditActionUpdated, ObjectType: AuditObjectTask}, "object_id"},
		{"对象类型未知", AuditEntry{ActorID: "x", ObjectID: "y", Action: AuditActionUpdated, ObjectType: "unknown"}, "object_type"},
		{"动作未知", AuditEntry{ActorID: "x", ObjectID: "y", Action: "unknown", ObjectType: AuditObjectTask}, "action"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAuditEntry(tc.entry)
			if err == nil {
				t.Fatal("非法记录未被拒绝")
			}
			domainErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("错误类型 = %T，期望 *domain.Error", err)
			}
			if _, ok := domainErr.Fields[tc.field]; !ok {
				t.Fatalf("错误未指出字段 %q，实际 fields = %v", tc.field, domainErr.Fields)
			}
		})
	}
}
