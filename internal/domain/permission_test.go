package domain

import "testing"

func TestSelfPermission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		actor  string
		target string
		want   Permission
	}{
		{name: "本人可写", actor: "u1", target: "u1", want: PermWrite},
		{name: "他人不可见", actor: "u1", target: "u2", want: PermNone},
		{name: "匿名不可见", actor: "", target: "u1", want: PermNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SelfPermission(tc.actor, tc.target); got != tc.want {
				t.Fatalf("SelfPermission(%q, %q) = %v，期望 %v", tc.actor, tc.target, got, tc.want)
			}
		})
	}
}

func TestPermissionAtLeast(t *testing.T) {
	t.Parallel()

	if !PermManage.AtLeast(PermRead) {
		t.Error("manage 应满足 read 要求")
	}
	if PermRead.AtLeast(PermWrite) {
		t.Error("read 不应满足 write 要求")
	}
	if PermNone.AtLeast(PermRead) {
		t.Error("none 不应满足任何要求")
	}
	if !PermRead.AtLeast(PermNone) {
		t.Error("任何权限都应满足 none 要求")
	}
}

func TestPermissionString(t *testing.T) {
	t.Parallel()

	tests := map[Permission]string{
		PermNone:   "none",
		PermRead:   "read",
		PermWrite:  "write",
		PermManage: "manage",
	}
	for permission, want := range tests {
		if got := permission.String(); got != want {
			t.Errorf("Permission(%d).String() = %q，期望 %q", permission, got, want)
		}
	}
}

func TestCanAdministerUsers(t *testing.T) {
	t.Parallel()

	if CanAdministerUsers(User{SystemRole: UserSystemRoleMember}) {
		t.Error("普通成员不应具备用户管理权限")
	}
	if !CanAdministerUsers(User{SystemRole: UserSystemRoleAdmin}) {
		t.Error("系统管理员应具备用户管理权限")
	}
}

func TestUserHelpers(t *testing.T) {
	t.Parallel()

	active := User{Status: UserStatusActive, SystemRole: UserSystemRoleMember}
	if !active.IsActive() {
		t.Error("active 用户应报告为可用")
	}
	if active.IsAdmin() {
		t.Error("member 不应报告为管理员")
	}

	disabled := User{Status: UserStatusDisabled, SystemRole: UserSystemRoleAdmin}
	if disabled.IsActive() {
		t.Error("disabled 用户不应报告为可用")
	}
	if !disabled.IsAdmin() {
		t.Error("admin 应报告为管理员，即使已被停用")
	}
}

func TestProjectVisibilityRules(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		ProjectVisibilityPrivate:       false,
		ProjectVisibilityGroupReadOnly: true,
		ProjectVisibilityGroupEditable: true,
	}
	for visibility, want := range tests {
		if got := ProjectVisibleToGroup(visibility); got != want {
			t.Errorf("ProjectVisibleToGroup(%q) = %v，期望 %v", visibility, got, want)
		}
	}

	// 任何共享级别都不授予项目权限管理（需求文档 4.3）。
	for _, visibility := range ProjectVisibilities {
		if CanManageProjectVisibility(visibility) {
			t.Errorf("共享级别 %q 不应授予共享权限管理能力", visibility)
		}
	}
}

// TestEnumWhitelistsMatchDatabaseConstraints 是本项目最重要的一致性测试之一：
// 领域白名单一旦与迁移中的 CHECK 约束漂移，就会出现"领域放行、数据库拒绝"的 500。
func TestEnumWhitelistsMatchDatabaseConstraints(t *testing.T) {
	t.Parallel()

	// 取值必须与 docs/02-数据库模型与迁移.md 的 CHECK 约束逐条一致。
	assertSameSet(t, "ProjectStatuses", ProjectStatuses,
		"not_started", "in_progress", "paused", "completed", "archived", "cancelled")
	assertSameSet(t, "ProjectVisibilities", ProjectVisibilities,
		"private", "group_readonly", "group_editable")
	assertSameSet(t, "TaskStatuses", TaskStatuses,
		"not_started", "in_progress", "completed", "cancelled")
	assertSameSet(t, "MemberRoles", MemberRoles, "owner", "member")
	assertSameSet(t, "MemberStatuses", MemberStatuses, "active", "removed")
}

// assertSameSet 断言两个集合完全相等且都无重复项。
func assertSameSet(t *testing.T, name string, got []string, want ...string) {
	t.Helper()

	gotSet := map[string]bool{}
	for _, v := range got {
		if gotSet[v] {
			t.Errorf("%s 存在重复取值 %q", name, v)
		}
		gotSet[v] = true
	}
	wantSet := map[string]bool{}
	for _, v := range want {
		wantSet[v] = true
	}

	for v := range wantSet {
		if !gotSet[v] {
			t.Errorf("%s 缺少取值 %q，实际为 %v", name, v, got)
		}
	}
	for v := range gotSet {
		if !wantSet[v] {
			t.Errorf("%s 存在多余取值 %q，期望集合为 %v", name, v, want)
		}
	}
	if len(gotSet) != len(wantSet) {
		t.Errorf("%s 去重后大小 = %d，期望 %d", name, len(gotSet), len(wantSet))
	}
}
