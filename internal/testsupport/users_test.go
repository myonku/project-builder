package testsupport

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/app/auth"
)

// Users 域的集成测试（接口契约 4.3）。
//
// 覆盖契约要求的七个维度：成功、未登录、无权限、对象不存在、参数错误、
// 重复请求（邮箱唯一）、版本冲突；另外覆盖两处本项目特有的不变式：
// "系统必须至少保留一个启用中的管理员"与"今天按请求者时区计算"。

// promoteToAdmin 直接把用户提升为系统管理员。
//
// 受支持的初始化入口是配置驱动的 auth.Service.EnsureAdministrator
// （见 TestBootstrapAdministratorUnlocksAdminEndpoints）；这个辅助函数保留下来
// 是为了在用例中**临时**造出第二个管理员等前置状态，比走初始化流程更直接。
// 它同时是"在引入初始化配置之前，只能这样造管理员"的可执行记录。
func promoteToAdmin(t *testing.T, harness *Harness, userID string) {
	t.Helper()

	tag, err := harness.Pool.Exec(context.Background(),
		`UPDATE users SET system_role = 'admin', updated_at = now() WHERE id = $1`, userID)
	if err != nil {
		t.Fatalf("提升为管理员失败: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("提升为管理员影响了 %d 行，期望 1 行（user_id=%s）", tag.RowsAffected(), userID)
	}
}

// countActiveAdmins 返回当前启用中的系统管理员数量。
func countActiveAdmins(t *testing.T, harness *Harness) int {
	t.Helper()

	var count int
	err := harness.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE system_role = 'admin' AND status = 'active'`).Scan(&count)
	if err != nil {
		t.Fatalf("统计启用中管理员失败: %v", err)
	}
	return count
}

// profileKeys 读取 /auth/me 或 /users/{id} 的 data 字段名集合。
func profileKeys(t *testing.T, response Response) []string {
	t.Helper()

	data := response.Data(t)
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestUserProfileVisibilityFollowsWorkspaceMembership 覆盖契约 4.3 的可见性规则：
// 本人、管理员、同组成员、同项目成员可见；无交集的用户必须得到 404（而不是 403），
// 否则任意登录用户就能用状态码差异枚举系统里有哪些账号。
func TestUserProfileVisibilityFollowsWorkspaceMembership(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_visible")
	bob := harness.RegisterAndLogin(t, "bob_visible")
	carol := harness.RegisterAndLogin(t, "carol_visible")
	admin := harness.RegisterAndLogin(t, "admin_visible")
	promoteToAdmin(t, harness, admin.UserID)

	// 本人可见。
	alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)

	// 尚未建立协作关系：互相不可见，且必须是 404 而不是 403。
	alice.Client.Get(URL("/users/"+bob.UserID)).RequireStatus(t, 404).
		RequireCode(t, "user_not_found")
	carol.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 404)

	// 同组之后互相可见。
	groupID := harness.CreateGroup(t, alice, "可见性工作组")
	harness.AddGroupMember(t, alice, groupID, bob.UserID)

	alice.Client.Get(URL("/users/"+bob.UserID)).RequireStatus(t, 200)
	bob.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)

	// 同项目（成员可以来自其他工作组）同样可见。
	projectID := harness.CreateProject(t, alice, groupID, "可见性项目")
	added := alice.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": carol.UserID})
	added.RequireStatus(t, 201)

	carol.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)

	// 系统管理员可以看到任何用户。
	admin.Client.Get(URL("/users/"+carol.UserID)).RequireStatus(t, 200)

	// 未登录：会话网关直接拦下。
	harness.Client(t).Get(URL("/users/"+alice.UserID)).RequireStatus(t, 401).
		RequireCode(t, "authentication_required")
}

// TestUserProfileUpdateRequiresSelfOrAdmin 覆盖修改权限与 404/403 的区分。
func TestUserProfileUpdateRequiresSelfOrAdmin(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_update")
	bob := harness.RegisterAndLogin(t, "bob_update")
	carol := harness.RegisterAndLogin(t, "carol_update")
	admin := harness.RegisterAndLogin(t, "admin_update")
	promoteToAdmin(t, harness, admin.UserID)

	groupID := harness.CreateGroup(t, alice, "更新权限工作组")
	harness.AddGroupMember(t, alice, groupID, bob.UserID)

	// 可见但无权修改：同组成员修改别人的资料必须是 403。
	bob.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"display_name":        "被同事改名",
		"expected_updated_at": "2026-10-11T09:30:00Z",
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 不可见：无交集的用户得到 404，不泄漏该账号是否存在。
	carol.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"display_name":        "陌生人改名",
		"expected_updated_at": "2026-10-11T09:30:00Z",
	}).RequireStatus(t, 404).RequireCode(t, "user_not_found")

	// 版本冲突：先用错误版本试探。
	conflict := alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"display_name":        "爱丽丝",
		"expected_updated_at": "2020-01-01T00:00:00Z",
	})
	conflict.RequireStatus(t, 409).RequireCode(t, "version_conflict")

	// 本人正常更新。
	current := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)
	updated := alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"display_name":        "爱丽丝",
		"timezone":            "Asia/Shanghai",
		"reminder_enabled":    false,
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	})
	updated.RequireStatus(t, 200)

	data := updated.Data(t)
	if got := Str(data, "display_name"); got != "爱丽丝" {
		t.Errorf("display_name = %q，期望 %q", got, "爱丽丝")
	}
	if got := Str(data, "timezone"); got != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望 %q", got, "Asia/Shanghai")
	}
	if got, ok := data["reminder_enabled"].(bool); !ok || got {
		t.Errorf("reminder_enabled = %v，期望 false", data["reminder_enabled"])
	}
	// username 不在契约字段集合内，任何情况下都不得被改动。
	if got := Str(data, "username"); got != alice.Username {
		t.Errorf("username = %q，期望保持 %q（用户名不可修改）", got, alice.Username)
	}

	// 管理员可以修改他人资料。
	adminCurrent := admin.Client.Get(URL("/users/"+bob.UserID)).RequireStatus(t, 200)
	admin.Client.Patch(URL("/users/"+bob.UserID), map[string]any{
		"display_name":        "Bob（管理员改名）",
		"expected_updated_at": Str(adminCurrent.Data(t), "updated_at"),
	}).RequireStatus(t, 200)
}

// TestUserProfileStatusAndRoleAreAdminOnly 覆盖"普通用户即使是本人也不能改
// status / system_role"，并确认错误带字段明细而不是静默忽略。
func TestUserProfileStatusAndRoleAreAdminOnly(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_role")

	current := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)
	version := Str(current.Data(t), "updated_at")

	alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"status":              "disabled",
		"expected_updated_at": version,
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied").RequireField(t, "status")

	alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"system_role":         "admin",
		"expected_updated_at": version,
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied").RequireField(t, "system_role")

	// 两次被拒绝之后，状态与角色都没有任何变化。
	unchanged := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)
	if got := Str(unchanged.Data(t), "status"); got != "active" {
		t.Errorf("status = %q，期望仍为 active", got)
	}
	if got := Str(unchanged.Data(t), "system_role"); got != "member" {
		t.Errorf("system_role = %q，期望仍为 member", got)
	}
}

// TestUserProfileUpdateAppliesEveryProvidedField 是本模块最重要的一条守卫。
//
// 第二阶段实测到的最危险缺陷是"归一化丢字段"：请求返回 200、updated_at 正常推进，
// 但目标字段静默未更新——没有错误码、没有 5xx，人工点测几乎必然漏过
// （docs/06-进度与验证记录.md 5.5 第 5 条）。因此这里一次性提供全部 6 个可改字段，
// 并逐字段断言它们**确实生效**，而不是只看状态码。
func TestUserProfileUpdateAppliesEveryProvidedField(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_fields")
	promoteToAdmin(t, harness, admin.UserID)
	subject := harness.RegisterAndLogin(t, "subject_fields")

	current := admin.Client.Get(URL("/users/"+subject.UserID)).RequireStatus(t, 200)
	before := Str(current.Data(t), "updated_at")

	updated := admin.Client.Patch(URL("/users/"+subject.UserID), map[string]any{
		"display_name":        "被改后的名字",
		"email":               "renamed_subject@example.com",
		"timezone":            "Asia/Shanghai",
		"reminder_enabled":    false,
		"status":              "disabled",
		"system_role":         "admin",
		"expected_updated_at": before,
	}).RequireStatus(t, 200)

	data := updated.Data(t)
	if got := Str(data, "display_name"); got != "被改后的名字" {
		t.Errorf("display_name = %q，期望 %q（字段被静默丢弃？）", got, "被改后的名字")
	}
	if got := Str(data, "email"); got != "renamed_subject@example.com" {
		t.Errorf("email = %q，期望 %q（字段被静默丢弃？）", got, "renamed_subject@example.com")
	}
	if got := Str(data, "timezone"); got != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望 %q（字段被静默丢弃？）", got, "Asia/Shanghai")
	}
	if got, ok := data["reminder_enabled"].(bool); !ok || got {
		t.Errorf("reminder_enabled = %v，期望 false（字段被静默丢弃？）", data["reminder_enabled"])
	}
	if got := Str(data, "status"); got != "disabled" {
		t.Errorf("status = %q，期望 disabled（字段被静默丢弃？）", got)
	}
	if got := Str(data, "system_role"); got != "admin" {
		t.Errorf("system_role = %q，期望 admin（字段被静默丢弃？）", got)
	}
	if got := Str(data, "updated_at"); got == before {
		t.Errorf("updated_at 未推进，仍为 %q", got)
	}

	// 重新读取一次：确认不是"只有响应体好看而库里没写"。
	reloaded := admin.Client.Get(URL("/users/"+subject.UserID)).RequireStatus(t, 200).Data(t)
	if got := Str(reloaded, "email"); got != "renamed_subject@example.com" {
		t.Errorf("重新读取的 email = %q，期望 %q", got, "renamed_subject@example.com")
	}
	if got := Str(reloaded, "system_role"); got != "admin" {
		t.Errorf("重新读取的 system_role = %q，期望 admin", got)
	}
}

// TestUserProfileUpdateRejectsEmptyAndInvalidPayload 覆盖参数错误维度。
func TestUserProfileUpdateRejectsEmptyAndInvalidPayload(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_payload")
	path := URL("/users/" + alice.UserID)

	current := alice.Client.Get(path).RequireStatus(t, 200)
	version := Str(current.Data(t), "updated_at")

	// 只有版本、没有任何可改字段：拒绝。否则一次没有变更的请求会推进 updated_at，
	// 让其他客户端手里的版本无谓失效。
	alice.Client.Patch(path, map[string]any{"expected_updated_at": version}).
		RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "body")

	// 未知字段（契约 2.7）：username 不可修改，拼写错误也必须立刻暴露。
	alice.Client.Patch(path, map[string]any{
		"username":            "renamed",
		"expected_updated_at": version,
	}).RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "username")

	// 字段级校验：非法时区、非法状态、空白显示名、邮箱格式。
	for name, body := range map[string]map[string]any{
		"时区非法": {
			"timezone":            "Mars/Olympus",
			"expected_updated_at": version,
		},
		"时区为空": {
			"timezone":            "",
			"expected_updated_at": version,
		},
		"邮箱格式非法": {
			"email":               "not-an-email",
			"expected_updated_at": version,
		},
		"显示名为空白": {
			"display_name":        "   ",
			"expected_updated_at": version,
		},
	} {
		field := map[string]string{
			"时区非法": "timezone", "时区为空": "timezone",
			"邮箱格式非法": "email", "显示名为空白": "display_name",
		}[name]
		alice.Client.Patch(path, body).RequireStatus(t, 422).
			RequireCode(t, "validation_error").RequireField(t, field)
	}

	// 缺少版本字段：契约 2.6 要求必填。
	alice.Client.Patch(path, map[string]any{"display_name": "无版本"}).
		RequireStatus(t, 422).RequireCode(t, "validation_error").
		RequireField(t, "expected_updated_at")

	// 路径参数不是合法 UUID：必须是 400，不能交给数据库退化成 500。
	alice.Client.Get(URL("/users/not-a-uuid")).RequireStatus(t, 400).
		RequireCode(t, "invalid_request")

	// 合法但不存在（且不可见）的 UUID：404。
	alice.Client.Get(URL("/users/00000000-0000-7000-8000-000000000000")).
		RequireStatus(t, 404).RequireCode(t, "user_not_found")
}

// TestUserProfileEmailUniquenessIsEnforcedByDatabase 覆盖"重复请求"维度：
// 邮箱唯一性由数据库唯一索引兜底，应用层的提前检查只是提示。
func TestUserProfileEmailUniquenessIsEnforcedByDatabase(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_email")
	bob := harness.RegisterAndLogin(t, "bob_email")

	current := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)
	// 大小写不同的同一邮箱同样必须被拒绝：唯一索引建在 lower(email) 上。
	alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"email":               "BOB_EMAIL@example.com",
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "email")

	// 自身邮箱改成同样的大小写变体不算冲突。
	again := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)
	alice.Client.Patch(URL("/users/"+alice.UserID), map[string]any{
		"email":               "ALICE_EMAIL@example.com",
		"expected_updated_at": Str(again.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	// bob 的邮箱没有被改动。
	unchanged := bob.Client.Get(URL("/users/"+bob.UserID)).RequireStatus(t, 200)
	if got := Str(unchanged.Data(t), "email"); got != bob.Email {
		t.Errorf("bob 的 email = %q，期望 %q", got, bob.Email)
	}
}

// TestUserProfileNeverExposesPasswordHash 锁住接口契约 3.1 末句：
// 密码、密码摘要与会话令牌永不返回。
func TestUserProfileNeverExposesPasswordHash(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_secret")
	response := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200)

	if _, ok := response.Data(t)["password_hash"]; ok {
		t.Fatal("用户资料响应泄漏了 password_hash")
	}
	if containsAnySecret(string(response.Body)) {
		t.Fatalf("响应体疑似包含密码或摘要字段: %s", string(response.Body))
	}
}

// TestUserProfileShapeMatchesAuthMe 是本模块与 auth 模块之间的**契约漂移守卫**。
//
// 两个模块各自定义公开用户结构（业务模块之间不得互相依赖），
// 因此这里显式断言两者的 JSON 字段集合完全一致：任何一侧改了字段而另一侧没跟上，
// 这条用例立即失败，而不是等到前端发现字段缺失。
func TestUserProfileShapeMatchesAuthMe(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "alice_shape")

	me := profileKeys(t, alice.Client.Get(URL("/auth/me")).RequireStatus(t, 200))
	profile := profileKeys(t, alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200))

	if len(me) != len(profile) {
		t.Fatalf("/auth/me 字段数 = %d，/users/{id} 字段数 = %d\nme      = %v\nprofile = %v",
			len(me), len(profile), me, profile)
	}
	for index := range me {
		if me[index] != profile[index] {
			t.Fatalf("字段集合不一致：/auth/me = %v，/users/{id} = %v", me, profile)
		}
	}
}

// TestDisableUserKeepsRowAndInvalidatesExistingSession 覆盖停用端点的全部要点：
// 仅管理员可调用、要求版本、拒绝重复停用、行保留、既有会话立即失效。
func TestDisableUserKeepsRowAndInvalidatesExistingSession(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_disable")
	promoteToAdmin(t, harness, admin.UserID)
	victim := harness.RegisterAndLogin(t, "victim_disable")
	other := harness.RegisterAndLogin(t, "other_disable")

	// 停用后既有会话必须立刻失效，因此先确认它现在可用。
	victim.Client.Get(URL("/auth/me")).RequireStatus(t, 200)

	// 非管理员不能停用任何人；目标不存在与否都返回同一个 403，
	// 因此这个端点不会变成"账号是否存在"的探测接口。
	other.Client.Post(URL("/users/"+victim.UserID+"/disable"),
		map[string]any{"expected_updated_at": "2026-10-11T09:30:00Z"}).
		RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 版本不匹配。
	admin.Client.Post(URL("/users/"+victim.UserID+"/disable"),
		map[string]any{"expected_updated_at": "2020-01-01T00:00:00Z"}).
		RequireStatus(t, 409).RequireCode(t, "version_conflict")

	victimProfile := admin.Client.Get(URL("/users/"+victim.UserID)).RequireStatus(t, 200)
	version := Str(victimProfile.Data(t), "updated_at")

	disabled := admin.Client.Post(URL("/users/"+victim.UserID+"/disable"),
		map[string]any{"expected_updated_at": version})
	disabled.RequireStatus(t, 200)
	if got := Str(disabled.Data(t), "status"); got != "disabled" {
		t.Fatalf("status = %q，期望 disabled", got)
	}

	// 数据保留：users 行仍然存在。
	var displayName string
	err := harness.Pool.QueryRow(context.Background(),
		`SELECT display_name FROM users WHERE id = $1`, victim.UserID).Scan(&displayName)
	if err != nil {
		t.Fatalf("停用后用户行应保留，查询失败: %v", err)
	}
	if displayName != victim.Username {
		t.Errorf("display_name = %q，期望 %q", displayName, victim.Username)
	}

	// 既有会话立即失效（契约 2.2：会话有效性每次请求实时回读数据库）。
	victim.Client.Get(URL("/auth/me")).RequireStatus(t, 401).
		RequireCode(t, "authentication_required")

	// 停用用户不能重新登录。
	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": victim.Username, "password": SharedPassword,
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 重复停用被拒绝，而不是"成功但没有变更"。
	repeated := admin.Client.Post(URL("/users/"+victim.UserID+"/disable"),
		map[string]any{
			"expected_updated_at": Str(disabled.Data(t), "updated_at"),
		})
	repeated.RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "status")
}

// TestLastActiveAdminCannotBeDemotedOrDisabled 覆盖本项目特有的防锁死不变式。
//
// 管理员是唯一能停用用户、修改系统角色和处理全局异常的入口；
// 允许把最后一个启用中的管理员降级或停用，系统会永久失去管理能力，
// 且没有任何接口可以恢复——只能直接改数据库。
func TestLastActiveAdminCannotBeDemotedOrDisabled(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_single")
	promoteToAdmin(t, harness, admin.UserID)

	if got := countActiveAdmins(t, harness); got != 1 {
		t.Fatalf("启用中管理员数量 = %d，期望 1", got)
	}

	selfDemote := func() Response {
		current := admin.Client.Get(URL("/users/"+admin.UserID)).RequireStatus(t, 200)
		return admin.Client.Patch(URL("/users/"+admin.UserID), map[string]any{
			"system_role":         "member",
			"expected_updated_at": Str(current.Data(t), "updated_at"),
		})
	}

	// 唯一管理员降级自己：拒绝。
	selfDemote().RequireStatus(t, 409).RequireCode(t, "conflict").
		RequireField(t, "system_role")

	// 唯一管理员停用自己：同样拒绝。
	current := admin.Client.Get(URL("/users/"+admin.UserID)).RequireStatus(t, 200)
	admin.Client.Patch(URL("/users/"+admin.UserID), map[string]any{
		"status":              "disabled",
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "status")

	if got := countActiveAdmins(t, harness); got != 1 {
		t.Fatalf("被拒绝后启用中管理员数量 = %d，期望仍为 1", got)
	}

	// 出现第二个管理员后，降级自己就被允许。
	second := harness.RegisterAndLogin(t, "admin_second")
	promoteToAdmin(t, harness, second.UserID)

	selfDemote().RequireStatus(t, 200)
	if got := countActiveAdmins(t, harness); got != 1 {
		t.Fatalf("降级后启用中管理员数量 = %d，期望 1", got)
	}

	// 现在 second 成了唯一管理员，它也不能再降级自己。
	secondCurrent := second.Client.Get(URL("/users/"+second.UserID)).RequireStatus(t, 200)
	second.Client.Patch(URL("/users/"+second.UserID), map[string]any{
		"system_role":         "member",
		"expected_updated_at": Str(secondCurrent.Data(t), "updated_at"),
	}).RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "system_role")
}

// TestConcurrentAdminDemotionKeepsAtLeastOneAdmin 验证"复查与写入同事务"的必要性。
//
// 两个管理员同时降级自己。若守卫是"先读计数、再无条件写入"，两个请求都会看到
// 对方仍是管理员而放行，结果是系统失去全部管理员。这里的断言与调度顺序无关：
// 必须恰好一个成功，且结束后仍有一个启用中的管理员。
//
// 为了让并发是**确定成立**的，用例先用一个独立会话占住启用中管理员的行锁，
// 强制两个请求在写入前排队。否则它们很可能被调度成一先一后，
// 即使守卫写成先读计数再写入也会侥幸通过——这是本用例最初的失败教训。
//
// 若更新路径没有获取行锁，请求会在锁仍被持有的瞬间就返回，用例立即失败。
// TestAdminGuardTakesRowLocksOnAllActiveAdmins 精确隔离"守卫是否真的取行锁"。
//
// 隔离手法是关键：若把**被更新的那一行**也锁住，那么即使守卫完全不加锁，
// UPDATE 自身也会阻塞，用例就无法区分"守卫加锁"与"写入加锁"。
// 因此这里只锁住**另一个**管理员：守卫必须遍历并锁定全部启用中管理员，
// 才会在它身上阻塞；若守卫是先读后写，它根本不会碰这一行，请求会立刻成功。
func TestAdminGuardTakesRowLocksOnAllActiveAdmins(t *testing.T) {
	harness := NewHarness(t)

	target := harness.RegisterAndLogin(t, "admin_lock_target")
	other := harness.RegisterAndLogin(t, "admin_lock_other")
	promoteToAdmin(t, harness, target.UserID)
	promoteToAdmin(t, harness, other.UserID)

	current := target.Client.Get(URL("/users/"+target.UserID)).RequireStatus(t, 200)
	version := Str(current.Data(t), "updated_at")

	ctx := context.Background()
	conn, err := harness.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取数据库连接失败: %v", err)
	}
	defer conn.Release()

	blocker, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("开启占锁事务失败: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	// 只锁住另一个管理员，不锁被更新的那一行。
	if _, err := blocker.Exec(ctx,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`, other.UserID); err != nil {
		t.Fatalf("占用另一个管理员的行锁失败: %v", err)
	}

	type outcome struct {
		status int
		body   []byte
	}
	result := make(chan outcome, 1)
	go func() {
		response := target.Client.Patch(URL("/users/"+target.UserID), map[string]any{
			"system_role":         "member",
			"expected_updated_at": version,
		})
		result <- outcome{status: response.Status, body: response.Body}
	}()

	select {
	case got := <-result:
		t.Fatalf("另一个管理员的行锁被占用期间，降级请求已经返回（状态码 %d，响应体 %s）："+
			"守卫没有锁定全部启用中管理员，先读后写无法阻止并发降级",
			got.status, string(got.body))
	case <-time.After(500 * time.Millisecond):
	}

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("释放行锁失败: %v", err)
	}

	select {
	case got := <-result:
		if got.status != 200 {
			t.Fatalf("放行后降级返回状态码 %d，响应体 %s", got.status, string(got.body))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("放行后降级请求未在超时内完成")
	}

	if got := countActiveAdmins(t, harness); got != 1 {
		t.Fatalf("降级后启用中管理员数量 = %d，期望 1（另一个管理员仍在职）", got)
	}
}

func TestAdminGuardSerializesConcurrentDemotions(t *testing.T) {
	harness := NewHarness(t)

	first := harness.RegisterAndLogin(t, "admin_race_one")
	second := harness.RegisterAndLogin(t, "admin_race_two")
	promoteToAdmin(t, harness, first.UserID)
	promoteToAdmin(t, harness, second.UserID)

	// 版本准备在主 goroutine 完成：goroutine 内不得调用 t.Fatalf。
	type prepared struct {
		actor   Authenticated
		version string
	}
	ready := make([]prepared, 0, 2)
	for _, actor := range []Authenticated{first, second} {
		current := actor.Client.Get(URL("/users/"+actor.UserID)).RequireStatus(t, 200)
		ready = append(ready, prepared{actor: actor, version: Str(current.Data(t), "updated_at")})
	}

	ctx := context.Background()
	conn, err := harness.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取数据库连接失败: %v", err)
	}
	defer conn.Release()

	blocker, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("开启占锁事务失败: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx,
		`SELECT id FROM users WHERE system_role = 'admin' AND status = 'active' ORDER BY id FOR UPDATE`,
	); err != nil {
		t.Fatalf("占用管理员行锁失败: %v", err)
	}

	type outcome struct {
		status int
		body   []byte
	}
	results := make(chan outcome, len(ready))
	for _, item := range ready {
		go func(item prepared) {
			response := item.actor.Client.Patch(URL("/users/"+item.actor.UserID), map[string]any{
				"system_role":         "member",
				"expected_updated_at": item.version,
			})
			results <- outcome{status: response.Status, body: response.Body}
		}(item)
	}

	// 行锁仍被外部事务持有时，任何请求都不得返回。
	select {
	case result := <-results:
		t.Fatalf("行锁被占用期间请求已返回（状态码 %d，响应体 %s）："+
			"说明复查没有经过行锁，先读后写会让并发的两次降级同时通过",
			result.status, string(result.body))
	case <-time.After(500 * time.Millisecond):
	}

	// 放行后再收集结果：必须恰好一个成功。
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("释放管理员行锁失败: %v", err)
	}

	succeeded, conflicted := 0, 0
	for range ready {
		select {
		case result := <-results:
			switch result.status {
			case 200:
				succeeded++
			case 409:
				conflicted++
			default:
				t.Fatalf("并发降级返回了非预期状态码 %d：%s", result.status, string(result.body))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("并发降级请求未在超时内完成")
		}
	}

	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("并发降级结果 = %d 成功 / %d 冲突，期望恰好 1 成功 1 冲突", succeeded, conflicted)
	}
	if got := countActiveAdmins(t, harness); got != 1 {
		t.Fatalf("并发降级后启用中管理员数量 = %d，期望 1（系统绝不能失去全部管理员）", got)
	}
}

// TestTodayFollowsRequesterTimezone 覆盖 Users 落地后替换掉的临时做法。
//
// 固定时钟为 2026-10-11T09:30:00Z。同一时刻在 Pacific/Honolulu（UTC-10）是
// 2026-10-10 23:30，因此"今天"比 UTC 早一天。同一个任务的 is_overdue 与项目的
// overdue_task_count 必须在两个用户眼里不同——这正是"按用户时区计算今天"的意义。
func TestTodayFollowsRequesterTimezone(t *testing.T) {
	harness := NewHarness(t)

	utcUser := harness.RegisterAndLoginWithTimezone(t, "tz_utc", "UTC")
	honolulu := harness.RegisterAndLoginWithTimezone(t, "tz_honolulu", "Pacific/Honolulu")

	groupID := harness.CreateGroup(t, utcUser, "时区工作组")
	harness.AddGroupMember(t, utcUser, groupID, honolulu.UserID)
	projectID := harness.CreateProject(t, utcUser, groupID, "时区项目")

	// 让 Honolulu 用户成为项目成员，否则私有项目对它不可见。
	utcUser.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": honolulu.UserID}).RequireStatus(t, 201)

	// 结束日期为 UTC 的"昨天"，但在 Honolulu 眼里仍是"今天"。
	taskID := harness.CreateTask(t, utcUser, projectID, "跨时区任务", "2026-10-09", "2026-10-10")

	utcTask := utcUser.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200).Data(t)
	if got, ok := utcTask["is_overdue"].(bool); !ok || !got {
		t.Errorf("UTC 用户看到 is_overdue = %v，期望 true（今天为 2026-10-11）", utcTask["is_overdue"])
	}

	hnlTask := honolulu.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200).Data(t)
	if got, ok := hnlTask["is_overdue"].(bool); !ok || got {
		t.Errorf("Honolulu 用户看到 is_overdue = %v，期望 false（当地今天为 2026-10-10）", hnlTask["is_overdue"])
	}

	utcProject := utcUser.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200).Data(t)
	if got, ok := utcProject["overdue_task_count"].(float64); !ok || got != 1 {
		t.Errorf("UTC 用户看到 overdue_task_count = %v，期望 1", utcProject["overdue_task_count"])
	}

	hnlProject := honolulu.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200).Data(t)
	if got, ok := hnlProject["overdue_task_count"].(float64); !ok || got != 0 {
		t.Errorf("Honolulu 用户看到 overdue_task_count = %v，期望 0", hnlProject["overdue_task_count"])
	}
}

// TestUserTimezoneChangeTakesEffectImmediately 验证时区是**每次请求**读取的，
// 而不是在会话里缓存：用户改完时区后，同一会话的下一次请求就按新时区判定。
func TestUserTimezoneChangeTakesEffectImmediately(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLoginWithTimezone(t, "tz_switch", "UTC")
	projectID := harness.CreateProject(t, actor, harness.CreateGroup(t, actor, "时区切换组"), "时区切换项目")
	taskID := harness.CreateTask(t, actor, projectID, "跨日任务", "2026-10-09", "2026-10-10")

	before := actor.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200).Data(t)
	if got, ok := before["is_overdue"].(bool); !ok || !got {
		t.Fatalf("UTC 下 is_overdue = %v，期望 true", before["is_overdue"])
	}

	current := actor.Client.Get(URL("/users/"+actor.UserID)).RequireStatus(t, 200)
	actor.Client.Patch(URL("/users/"+actor.UserID), map[string]any{
		"timezone":            "Pacific/Honolulu",
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	after := actor.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200).Data(t)
	if got, ok := after["is_overdue"].(bool); !ok || got {
		t.Fatalf("改时区后 is_overdue = %v，期望 false（当地今天为 2026-10-10）", after["is_overdue"])
	}
}

// TestInvalidTimezoneDoesNotBreakReadPaths 锁住一个刻意的降级选择：
// 用户时区不可用时，读取路径必须仍然可用——"今天"只影响派生展示字段，
// 让一次列表请求因为一个展示值而 500 是更差的结果。
//
// 非法时区在接口层不可能写入（写入时返回 422），因此这里直接写库构造
// "历史数据或人工修改"的场景。
func TestInvalidTimezoneDoesNotBreakReadPaths(t *testing.T) {
	harness := NewHarness(t)

	actor := harness.RegisterAndLoginWithTimezone(t, "tz_invalid", "UTC")
	groupID := harness.CreateGroup(t, actor, "非法时区组")
	projectID := harness.CreateProject(t, actor, groupID, "非法时区项目")
	harness.CreateTask(t, actor, projectID, "非法时区任务", "2026-10-09", "2026-10-10")

	if _, err := harness.Pool.Exec(context.Background(),
		`UPDATE users SET timezone = 'Not/AZone' WHERE id = $1`, actor.UserID); err != nil {
		t.Fatalf("写入非法时区失败: %v", err)
	}

	// 读取路径必须仍然可用：仅派生字段回退到 UTC 日历日。
	task := actor.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
		map[string]any{"page": 1, "page_size": 20}).RequireStatus(t, 200)
	items := task.ListData(t)
	if len(items) != 1 {
		t.Fatalf("任务列表条目数 = %d，期望 1", len(items))
	}
	// 回退到 UTC 后今天仍是 2026-10-11，因此 end_date=2026-10-10 的任务应判为逾期。
	if got, ok := items[0]["is_overdue"].(bool); !ok || !got {
		t.Errorf("回退到 UTC 后 is_overdue = %v，期望 true", items[0]["is_overdue"])
	}

	actor.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200)
	actor.Client.Get(URL("/users/"+actor.UserID)).RequireStatus(t, 200)
}

// ---------------------------------------------------------------------------
// 管理员端点：创建用户 / 用户列表 / 重置密码
// ---------------------------------------------------------------------------

// createUserBody 是管理员创建用户的请求体。
func createUserBody(username string) map[string]any {
	return map[string]any{
		"username":     username,
		"email":        username + "@example.com",
		"display_name": username,
		"password":     SharedPassword,
		"timezone":     "Asia/Shanghai",
	}
}

// TestAdminCreateUserEqualsSelfRegistration 覆盖"管理员代建账号"。
//
// 关键点是它必须与自助注册**行为一致**：固定 active/member/reminder_enabled，
// 不自动登录、不设置会话 Cookie、不泄漏任何凭据，且字段校验与唯一性检查同一套。
func TestAdminCreateUserEqualsSelfRegistration(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_create")
	promoteToAdmin(t, harness, admin.UserID)
	plain := harness.RegisterAndLogin(t, "plain_create")

	// 非管理员不能创建账号。
	plain.Client.Post(URL("/users"), createUserBody("forbidden_create")).
		RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 管理员创建成功。
	created := admin.Client.Post(URL("/users"), createUserBody("created_by_admin"))
	created.RequireStatus(t, 201)

	data := created.Data(t)
	if got := Str(data, "status"); got != "active" {
		t.Errorf("status = %q，期望 active", got)
	}
	if got := Str(data, "system_role"); got != "member" {
		t.Errorf("system_role = %q，期望 member（管理员不能借创建直接造出第二个管理员）", got)
	}
	if got, ok := data["reminder_enabled"].(bool); !ok || !got {
		t.Errorf("reminder_enabled = %v，期望 true", data["reminder_enabled"])
	}
	if got := Str(data, "timezone"); got != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望 Asia/Shanghai", got)
	}
	// 响应必须是公开用户结构，不含任何凭据。
	if containsAnySecret(string(created.Body)) {
		t.Fatalf("创建用户响应疑似包含凭据字段: %s", string(created.Body))
	}
	// 契约 4.2：注册不自动登录。管理员代建同样不得设置会话 Cookie。
	for _, cookie := range created.Recorder.Result().Cookies() {
		if cookie.Name == harness.Config.Security.SessionCookieName && cookie.Value != "" {
			t.Fatalf("代建账号不应设置会话 Cookie，实际设置了 %q", cookie.Name)
		}
	}

	// 被创建的账号能用给定密码登录：证明了归一化与摘要写入都真实生效。
	login := harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": "created_by_admin", "password": SharedPassword,
	})
	login.RequireStatus(t, 200)
	if got := Str(login.Data(t), "id"); got != Str(data, "id") {
		t.Errorf("登录返回的用户 id = %q，期望 %q", got, Str(data, "id"))
	}

	// 唯一性由数据库唯一索引兜底，且必须是 409 + 字段明细。
	admin.Client.Post(URL("/users"), createUserBody("created_by_admin")).
		RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "username")

	duplicateEmail := createUserBody("another_admin_created")
	duplicateEmail["email"] = "CREATED_BY_ADMIN@example.com"
	admin.Client.Post(URL("/users"), duplicateEmail).
		RequireStatus(t, 409).RequireCode(t, "conflict").RequireField(t, "email")

	// 字段校验与注册共用同一套规则。
	weak := createUserBody("weak_password_user")
	weak["password"] = "short"
	admin.Client.Post(URL("/users"), weak).
		RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "password")

	// 未知字段（system_role 不在创建字段集合内）必须被拒绝，而不是被静默忽略。
	escalate := createUserBody("escalate_admin")
	escalate["system_role"] = "admin"
	admin.Client.Post(URL("/users"), escalate).
		RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "system_role")
}

// TestUserListRequiresAdminAndHonoursIncludeDisabled 覆盖用户列表端点。
func TestUserListRequiresAdminAndHonoursIncludeDisabled(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_list")
	promoteToAdmin(t, harness, admin.UserID)
	plain := harness.RegisterAndLogin(t, "plain_list")
	victim := harness.RegisterAndLogin(t, "victim_list")

	// 非管理员：403。普通成员能看到的应当是成员列表，而不是全系统账号清单。
	plain.Client.Post(URL("/users/query"), map[string]any{}).
		RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 默认排除停用账号。
	beforeDisable := admin.Client.Post(URL("/users/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	baseline := beforeDisable.Pagination(t)["total"].(float64)
	if int(baseline) < 3 {
		t.Fatalf("列表 total = %v，期望至少包含 3 个已注册用户", baseline)
	}
	if len(beforeDisable.ListData(t)) != int(baseline) {
		t.Fatalf("列表条目数 %d 与 total %v 不符（计数与列表必须复用同一套筛选）",
			len(beforeDisable.ListData(t)), baseline)
	}

	// 停用一个账号。
	current := admin.Client.Get(URL("/users/"+victim.UserID)).RequireStatus(t, 200)
	admin.Client.Post(URL("/users/"+victim.UserID+"/disable"),
		map[string]any{"expected_updated_at": Str(current.Data(t), "updated_at")}).
		RequireStatus(t, 200)

	// 停用后默认列表里查不到它。
	hidden := admin.Client.Post(URL("/users/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	if containsUser(hidden.ListData(t), victim.UserID) {
		t.Error("默认列表不应包含已停用账号")
	}
	// 默认口径的 total 也必须是"启用中账号数"，并与它返回的条目数一致。
	hiddenTotal := hidden.Pagination(t)["total"].(float64)
	if int(hiddenTotal) != int(baseline)-1 {
		t.Errorf("停用 1 个账号后默认 total = %v，期望 %d", hiddenTotal, int(baseline)-1)
	}
	if len(hidden.ListData(t)) != int(hiddenTotal) {
		t.Errorf("默认列表条目数 %d 与 total %v 不符", len(hidden.ListData(t)), hiddenTotal)
	}

	// 显式 include_disabled=true 时必须能查到：停用是数据保留，不是删除。
	shown := admin.Client.Post(URL("/users/query"),
		map[string]any{"include_disabled": true, "page": 1, "page_size": 100}).RequireStatus(t, 200)
	if !containsUser(shown.ListData(t), victim.UserID) {
		t.Error("include_disabled=true 时列表必须包含已停用账号")
	}
	shownTotal := shown.Pagination(t)["total"].(float64)
	if int(shownTotal) != int(baseline) {
		t.Errorf("include_disabled=true 的 total = %v，期望 %d（等于停用前的账号总数）",
			shownTotal, int(baseline))
	}
	if len(shown.ListData(t)) != int(shownTotal) {
		t.Errorf("include_disabled 列表条目数 %d 与 total %v 不符",
			len(shown.ListData(t)), shownTotal)
	}

	// 排序白名单：未知字段 400，合法字段按方向生效。
	admin.Client.Post(URL("/users/query"), map[string]any{"sort": "nonsense"}).
		RequireStatus(t, 400).RequireCode(t, "invalid_request")
	admin.Client.Post(URL("/users/query"), map[string]any{"sort": "password_hash"}).
		RequireStatus(t, 400).RequireCode(t, "invalid_request")
	admin.Client.Post(URL("/users/query"), map[string]any{"page_size": 500}).
		RequireStatus(t, 400).RequireCode(t, "invalid_request")

	sorted := admin.Client.Post(URL("/users/query"),
		map[string]any{"sort": "username", "page": 1, "page_size": 100}).RequireStatus(t, 200)
	items := sorted.ListData(t)
	for index := 1; index < len(items); index++ {
		if Str(items[index-1], "username") > Str(items[index], "username") {
			t.Fatalf("按 username 升序排列不正确：%q 出现在 %q 之前",
				Str(items[index-1], "username"), Str(items[index], "username"))
		}
	}
}

// TestAdminResetPassword 覆盖重置密码端点。
//
// 同时把"重置不吊销既有会话"这一**已知限制**变成被测试的行为，
// 而不是没人知道的意外：若日后加上会话吊销，这条断言会失败，
// 从而强制同时更新文档（docs/06 7.4 第 1 项）。
func TestAdminResetPassword(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "admin_reset")
	promoteToAdmin(t, harness, admin.UserID)
	plain := harness.RegisterAndLogin(t, "plain_reset")
	victim := harness.RegisterAndLogin(t, "victim_reset")

	path := URL("/users/" + victim.UserID + "/password")

	// 非管理员：403（先判权限再查对象，因此这个端点不会泄漏账号是否存在）。
	plain.Client.Post(path, map[string]any{
		"new_password": "brand new password", "expected_updated_at": "2026-10-11T09:30:00Z",
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied")

	plain.Client.Post(URL("/users/00000000-0000-7000-8000-000000000000/password"), map[string]any{
		"new_password": "brand new password", "expected_updated_at": "2026-10-11T09:30:00Z",
	}).RequireStatus(t, 403).RequireCode(t, "permission_denied")

	// 目标不存在：404。
	admin.Client.Post(URL("/users/00000000-0000-7000-8000-000000000000/password"), map[string]any{
		"new_password": "brand new password", "expected_updated_at": "2026-10-11T09:30:00Z",
	}).RequireStatus(t, 404).RequireCode(t, "user_not_found")

	current := admin.Client.Get(URL("/users/"+victim.UserID)).RequireStatus(t, 200)
	version := Str(current.Data(t), "updated_at")

	// 密码强度与注册同一套规则。
	admin.Client.Post(path, map[string]any{
		"new_password": "short", "expected_updated_at": version,
	}).RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "new_password")

	// 缺版本字段。
	admin.Client.Post(path, map[string]any{"new_password": "brand new password"}).
		RequireStatus(t, 422).RequireCode(t, "validation_error").
		RequireField(t, "expected_updated_at")

	// 过期版本：409。
	admin.Client.Post(path, map[string]any{
		"new_password": "brand new password", "expected_updated_at": "2020-01-01T00:00:00Z",
	}).RequireStatus(t, 409).RequireCode(t, "version_conflict")

	const newPassword = "brand new password"
	reset := admin.Client.Post(path, map[string]any{
		"new_password": newPassword, "expected_updated_at": version,
	})
	reset.RequireStatus(t, 200)

	// 响应是公开用户结构：绝不回显明文密码或摘要。
	if containsAnySecret(string(reset.Body)) || strings.Contains(string(reset.Body), newPassword) {
		t.Fatalf("重置密码响应泄漏了凭据: %s", string(reset.Body))
	}
	if got := Str(reset.Data(t), "updated_at"); got == version {
		t.Error("重置密码必须推进 updated_at")
	}

	// 新密码可用，旧密码失效。
	victim.Client.Post(URL("/auth/logout"), nil).RequireStatus(t, 204)
	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": victim.Username, "password": SharedPassword,
	}).RequireStatus(t, 401).RequireCode(t, "invalid_credentials")
	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": victim.Username, "password": newPassword,
	}).RequireStatus(t, 200)

	// **已知限制**：重置不吊销既有会话。
	//
	// 上面那次 logout 已撤销旧会话，因此这里重新建立一条会话再重置一次，
	// 以证明"改动密码不会让已发出的会话失效"。
	active := harness.RegisterAndLogin(t, "session_reset")
	active.Client.Get(URL("/auth/me")).RequireStatus(t, 200)

	activeCurrent := admin.Client.Get(URL("/users/"+active.UserID)).RequireStatus(t, 200)
	admin.Client.Post(URL("/users/"+active.UserID+"/password"), map[string]any{
		"new_password":        "another brand new password",
		"expected_updated_at": Str(activeCurrent.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	active.Client.Get(URL("/auth/me")).RequireStatus(t, 200)
	// 若需要立即踢下线，现行做法是先停用再启用（停用会让既有会话立即失效）。
	disableVersion := Str(admin.Client.Get(URL("/users/"+active.UserID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Post(URL("/users/"+active.UserID+"/disable"),
		map[string]any{"expected_updated_at": disableVersion}).RequireStatus(t, 200)
	active.Client.Get(URL("/auth/me")).RequireStatus(t, 401)
}

// TestBootstrapAdministratorUnlocksAdminEndpoints 证明"首个管理员"这条链路已经闭合。
//
// 在此之前，所有管理员用例都必须先用 SQL 把账号提升为 admin（见 promoteToAdmin），
// 因为系统没有任何受支持的方式造出第一个管理员——管理员端点因此在真实部署中不可用。
// 现在初始化入口是配置驱动（`security.bootstrap_admin_*` ⇒
// auth.Service.EnsureAdministrator），本用例全程不碰 SQL：
// 初始化 → 用初始化凭据登录 → 创建用户 → 列表 → 重置密码。
func TestBootstrapAdministratorUnlocksAdminEndpoints(t *testing.T) {
	harness := NewHarness(t)

	const (
		username = "bootstrap_admin_e2e"
		password = "bootstrap-admin-password"
	)
	ctx := context.Background()

	first, err := harness.Auth.EnsureAdministrator(ctx, auth.BootstrapAdminInput{
		Username: username,
		Email:    username + "@example.com",
		Password: password,
	})
	if err != nil {
		t.Fatalf("初始化引导管理员失败: %v", err)
	}
	if !first.Created {
		t.Fatalf("首次初始化应创建账号，实际为 %+v", first)
	}

	// 用初始化凭据登录，拿到一条真实会话。
	anonymous := harness.Client(t)
	login := anonymous.Post(URL("/auth/login"), map[string]any{
		"identifier": username, "password": password,
	})
	login.RequireStatus(t, 200)
	cookieName := harness.Config.Security.SessionCookieName
	cookieValue := ""
	for _, cookie := range login.Recorder.Result().Cookies() {
		if cookie.Name == cookieName {
			cookieValue = cookie.Value
		}
	}
	if cookieValue == "" {
		t.Fatalf("初始化管理员登录未返回会话 Cookie")
	}
	admin := harness.Client(t).WithCookie(cookieName, cookieValue)

	// 1) 创建用户。
	created := admin.Post(URL("/users"), createUserBody("created_by_bootstrap"))
	created.RequireStatus(t, 201)
	createdID := Str(created.Data(t), "id")
	if got := Str(created.Data(t), "system_role"); got != "member" {
		t.Errorf("新建账号的 system_role = %q，期望 member", got)
	}

	// 2) 列表：默认口径能查到刚创建的账号。
	listed := admin.Post(URL("/users/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	if !containsUser(listed.ListData(t), createdID) {
		t.Fatal("用户列表应包含刚创建的账号")
	}

	// 3) 重置密码：新密码可登录，旧密码失效。
	const newPassword = "reset by bootstrap admin"
	admin.Post(URL("/users/"+createdID+"/password"), map[string]any{
		"new_password":        newPassword,
		"expected_updated_at": Str(created.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": "created_by_bootstrap", "password": SharedPassword,
	}).RequireStatus(t, 401)
	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": "created_by_bootstrap", "password": newPassword,
	}).RequireStatus(t, 200)

	// 4) 再次初始化（模拟"下次启动"）绝不可覆盖管理员密码。
	//    这是整条链路里最容易造成永久性事故的一步：配置里的初始化密码通常
	//    在账号建好后就被移除，一旦重启覆盖密码，所有人都进不去。
	second, err := harness.Auth.EnsureAdministrator(ctx, auth.BootstrapAdminInput{
		Username: username,
		Email:    username + "@example.com",
		Password: "a-completely-different-password",
	})
	if err != nil {
		t.Fatalf("重复初始化失败: %v", err)
	}
	if second.Created || second.Promoted {
		t.Fatalf("重复初始化不应产生变更，实际为 %+v", second)
	}

	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": username, "password": password,
	}).RequireStatus(t, 200)
	harness.Client(t).Post(URL("/auth/login"), map[string]any{
		"identifier": username, "password": "a-completely-different-password",
	}).RequireStatus(t, 401)
}

// containsUser 报告列表中是否含指定用户的 id。
func containsUser(items []map[string]any, userID string) bool {
	for _, item := range items {
		if Str(item, "id") == userID {
			return true
		}
	}
	return false
}

// containsAnySecret 粗粒度检查响应体是否出现密码相关字样。
func containsAnySecret(body string) bool {
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return true
	}
	return scanForSecret(payload)
}

// scanForSecret 递归查找敏感键名。
func scanForSecret(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch key {
			case "password", "password_hash", "token", "session_token":
				return true
			}
			if scanForSecret(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if scanForSecret(item) {
				return true
			}
		}
	}
	return false
}
