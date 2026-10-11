package domain

import (
	"testing"
	"time"
)

// TestNormalizeGroupCreate 覆盖工作组创建输入校验。
func TestNormalizeGroupCreate(t *testing.T) {
	t.Parallel()

	valid := GroupMembershipInput{Name: "研发组"}

	t.Run("接受合法输入并去空格", func(t *testing.T) {
		t.Parallel()
		got, fields := NormalizeGroupCreate(GroupMembershipInput{
			Name:        "  研发组  ",
			Description: stringPtr("  负责核心系统  "),
		})
		if len(fields) != 0 {
			t.Fatalf("期望无字段错误，实际 %v", fields)
		}
		if got.Name != "研发组" {
			t.Errorf("name = %q，期望去空格", got.Name)
		}
		if got.Description == nil || *got.Description != "负责核心系统" {
			t.Errorf("description 未正确清洗: %v", got.Description)
		}
	})

	t.Run("空描述归一化为 nil", func(t *testing.T) {
		t.Parallel()
		got, fields := NormalizeGroupCreate(GroupMembershipInput{
			Name: "研发组", Description: stringPtr("   "),
		})
		if len(fields) != 0 {
			t.Fatalf("期望无字段错误，实际 %v", fields)
		}
		if got.Description != nil {
			t.Errorf("空白描述应归一化为 nil，实际 %q", *got.Description)
		}
	})

	tests := []struct {
		name      string
		mutate    func(in GroupMembershipInput) GroupMembershipInput
		wantField string
	}{
		{
			name:      "名称为空",
			mutate:    func(in GroupMembershipInput) GroupMembershipInput { in.Name = "  "; return in },
			wantField: "name",
		},
		{
			name: "名称过长",
			mutate: func(in GroupMembershipInput) GroupMembershipInput {
				long := make([]rune, GroupNameMaxLength+1)
				for i := range long {
					long[i] = '名'
				}
				in.Name = string(long)
				return in
			},
			wantField: "name",
		},
		{
			name: "描述过长",
			mutate: func(in GroupMembershipInput) GroupMembershipInput {
				long := make([]rune, GroupDescriptionMaxLength+1)
				for i := range long {
					long[i] = '述'
				}
				in.Description = stringPtr(string(long))
				return in
			},
			wantField: "description",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, fields := NormalizeGroupCreate(tc.mutate(valid))
			if _, ok := fields[tc.wantField]; !ok {
				t.Fatalf("期望字段 %q 报错，实际 %v", tc.wantField, fields)
			}
		})
	}
}

// TestCanAccessGroup 覆盖"审核通过前不能访问组内计划"。
func TestCanAccessGroup(t *testing.T) {
	t.Parallel()

	active := Group{ID: "g1", Status: GroupStatusActive, OwnerID: "u1"}
	disabled := Group{ID: "g1", Status: GroupStatusDisabled, OwnerID: "u1"}

	tests := []struct {
		name       string
		group      Group
		membership *GroupMember
		want       bool
	}{
		{name: "活跃成员可访问", group: active,
			membership: &GroupMember{Role: MemberRoleMember, Status: MemberStatusActive}, want: true},
		{name: "被移除成员不可访问", group: active,
			membership: &GroupMember{Role: MemberRoleMember, Status: MemberStatusRemoved}, want: false},
		{name: "非成员不可访问", group: active, membership: nil, want: false},
		{name: "停用组不可访问", group: disabled,
			membership: &GroupMember{Role: MemberRoleManager, Status: MemberStatusActive}, want: false},
		// 关键：所有者身份不构成访问豁免，他必须是活跃成员。
		{name: "所有者但成员关系非活跃", group: active,
			membership: &GroupMember{UserID: "u1", Role: MemberRoleManager, Status: MemberStatusRemoved}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanAccessGroup(tc.group, tc.membership); got != tc.want {
				t.Fatalf("CanAccessGroup = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestCanManageGroupMembers(t *testing.T) {
	t.Parallel()

	group := Group{ID: "g1", Status: GroupStatusActive, OwnerID: "owner"}

	tests := []struct {
		name       string
		membership *GroupMember
		actorID    string
		want       bool
	}{
		{name: "所有者可管理", actorID: "owner", want: true},
		{name: "组管理员可管理", actorID: "manager",
			membership: &GroupMember{Role: MemberRoleManager, Status: MemberStatusActive}, want: true},
		{name: "普通成员不可管理", actorID: "member",
			membership: &GroupMember{Role: MemberRoleMember, Status: MemberStatusActive}, want: false},
		{name: "非成员不可管理", actorID: "stranger", want: false},
		{name: "被移除的组管理员不可管理", actorID: "manager",
			membership: &GroupMember{Role: MemberRoleManager, Status: MemberStatusRemoved}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanManageGroupMembers(group, tc.membership, tc.actorID); got != tc.want {
				t.Fatalf("CanManageGroupMembers = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestCanUpdateGroupAllowsSystemAdmin(t *testing.T) {
	t.Parallel()

	group := Group{ID: "g1", Status: GroupStatusActive, OwnerID: "owner"}
	// 需求文档 5.2：系统管理员可以修改和停用工作组。
	admin := User{ID: "admin", SystemRole: UserSystemRoleAdmin}
	if !CanUpdateGroup(group, admin, nil) {
		t.Fatal("系统管理员应可修改工作组，即使不是成员")
	}

	// 普通非成员不可修改。
	member := User{ID: "stranger", SystemRole: UserSystemRoleMember}
	if CanUpdateGroup(group, member, nil) {
		t.Fatal("普通非成员不应可修改工作组")
	}
}

// TestValidateGroupMemberChange 覆盖 owner 不变式（docs/02 §3.2.1 保障 2）。
func TestValidateGroupMemberChange(t *testing.T) {
	t.Parallel()

	group := Group{ID: "g1", Status: GroupStatusActive, OwnerID: "owner"}
	ownerMember := GroupMember{UserID: "owner", Role: MemberRoleManager, Status: MemberStatusActive}
	plainMember := GroupMember{UserID: "member", Role: MemberRoleMember, Status: MemberStatusActive}
	managerMember := GroupMember{UserID: "manager", Role: MemberRoleManager, Status: MemberStatusActive}
	removedMember := GroupMember{UserID: "member", Role: MemberRoleMember, Status: MemberStatusRemoved}

	tests := []struct {
		name    string
		target  GroupMember
		action  string
		wantErr bool
	}{
		{name: "不能降级所有者", target: ownerMember, action: GroupMemberActionDemote, wantErr: true},
		{name: "不能移除所有者", target: ownerMember, action: GroupMemberActionRemove, wantErr: true},
		{name: "可以降级普通管理员", target: managerMember, action: GroupMemberActionDemote},
		{name: "可以移除普通成员", target: plainMember, action: GroupMemberActionRemove},
		{name: "可以提升普通成员", target: plainMember, action: GroupMemberActionPromote},
		{name: "不能提升已是管理员的人", target: managerMember, action: GroupMemberActionPromote, wantErr: true},
		{name: "不能降级非管理员", target: plainMember, action: GroupMemberActionDemote, wantErr: true},
		{name: "不能移除已移除的成员", target: removedMember, action: GroupMemberActionRemove, wantErr: true},
		{name: "可以恢复已移除成员", target: removedMember, action: GroupMemberActionRestore},
		{name: "不能恢复活跃成员", target: plainMember, action: GroupMemberActionRestore, wantErr: true},
		{name: "不支持的动作", target: plainMember, action: "promote_to_admin", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateGroupMemberChange(group, tc.target, tc.action)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

func TestValidateJoinRequestDecision(t *testing.T) {
	t.Parallel()

	pending := GroupJoinRequest{Status: JoinRequestPending}
	approved := GroupJoinRequest{Status: JoinRequestApproved}
	rejected := GroupJoinRequest{Status: JoinRequestRejected}

	tests := []struct {
		name     string
		current  GroupJoinRequest
		decision string
		wantErr  bool
	}{
		{name: "待审核可通过", current: pending, decision: JoinRequestApproved},
		{name: "待审核可拒绝", current: pending, decision: JoinRequestRejected},
		{name: "已通过不能重复审核", current: approved, decision: JoinRequestApproved, wantErr: true},
		{name: "已拒绝不能重复审核", current: rejected, decision: JoinRequestRejected, wantErr: true},
		{name: "结果不能是 pending", current: pending, decision: JoinRequestPending, wantErr: true},
		{name: "结果不能是其它值", current: pending, decision: "maybe", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateJoinRequestDecision(tc.current, tc.decision)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

// TestProjectPermission 覆盖项目权限矩阵（需求文档 4.2 / 4.3）。
func TestProjectPermission(t *testing.T) {
	t.Parallel()

	activeGroup := Group{ID: "g1", Status: GroupStatusActive}
	disabledGroup := Group{ID: "g1", Status: GroupStatusDisabled}
	activeGroupMember := &GroupMember{GroupID: "g1", Status: MemberStatusActive}
	ownerMember := &ProjectMember{ProjectID: "p1", UserID: "owner", Role: MemberRoleOwner}
	plainMember := &ProjectMember{ProjectID: "p1", UserID: "member", Role: MemberRoleMember}

	tests := []struct {
		name            string
		visibility      string
		member          *ProjectMember
		group           Group
		groupMembership *GroupMember
		want            Permission
	}{
		{name: "持有者可管理", visibility: ProjectVisibilityPrivate,
			member: ownerMember, group: activeGroup, want: PermManage},
		{name: "项目成员可写", visibility: ProjectVisibilityPrivate,
			member: plainMember, group: activeGroup, want: PermWrite},
		{name: "私有项目非成员不可见", visibility: ProjectVisibilityPrivate,
			group: activeGroup, groupMembership: activeGroupMember, want: PermNone},
		{name: "组内只读给读权限", visibility: ProjectVisibilityGroupReadOnly,
			group: activeGroup, groupMembership: activeGroupMember, want: PermRead},
		{name: "组内可编辑给写权限", visibility: ProjectVisibilityGroupEditable,
			group: activeGroup, groupMembership: activeGroupMember, want: PermWrite},
		{name: "停用组的成员无权限", visibility: ProjectVisibilityGroupEditable,
			group: disabledGroup, groupMembership: activeGroupMember, want: PermNone},
		{name: "被移除的组员无权限", visibility: ProjectVisibilityGroupEditable,
			group:           activeGroup,
			groupMembership: &GroupMember{GroupID: "g1", Status: MemberStatusRemoved}, want: PermNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := Project{ID: "p1", GroupID: "g1", Visibility: tc.visibility, Status: ProjectStatusInProgress}
			got := ProjectPermission(project, User{ID: "actor"}, tc.member, tc.group, tc.groupMembership)
			if got != tc.want {
				t.Fatalf("ProjectPermission = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestCanEditProjectTasksHonoursArchive 覆盖归档项目只读。
//
// 这是 Python 版本的实测缺陷：归档后仍可修改任务。
func TestCanEditProjectTasksHonoursArchive(t *testing.T) {
	t.Parallel()

	active := Project{Status: ProjectStatusInProgress}
	archivedByStatus := Project{Status: ProjectStatusArchived}
	archivedByTimestamp := Project{Status: ProjectStatusInProgress, ArchivedAt: timePtr(MustTimestamp("2026-10-01T00:00:00Z"))}

	if !CanEditProjectTasks(active, PermManage) {
		t.Error("进行中项目应可编辑任务")
	}
	if CanEditProjectTasks(archivedByStatus, PermManage) {
		t.Error("status=archived 的项目不得编辑任务")
	}
	if CanEditProjectTasks(archivedByTimestamp, PermManage) {
		t.Error("archived_at 非空的项目不得编辑任务")
	}
}

// TestValidateProjectMemberChange 覆盖"不得移除唯一 owner"。
func TestValidateProjectMemberChange(t *testing.T) {
	t.Parallel()

	owner := ProjectMember{UserID: "owner", Role: MemberRoleOwner}
	member := ProjectMember{UserID: "member", Role: MemberRoleMember}

	tests := []struct {
		name       string
		target     ProjectMember
		action     string
		ownerCount int
		wantErr    bool
	}{
		{name: "唯一 owner 不可移除", target: owner, action: GroupMemberActionRemove, ownerCount: 1, wantErr: true},
		{name: "非唯一 owner 可移除", target: owner, action: GroupMemberActionRemove, ownerCount: 2},
		{name: "普通成员可移除", target: member, action: GroupMemberActionRemove, ownerCount: 1},
		{name: "唯一 owner 不可降级", target: owner, action: GroupMemberActionDemote, ownerCount: 1, wantErr: true},
		{name: "非唯一 owner 可降级", target: owner, action: GroupMemberActionDemote, ownerCount: 2},
		{name: "普通成员不可降级", target: member, action: GroupMemberActionDemote, ownerCount: 1, wantErr: true},
		{name: "已是 owner 不可再提升", target: owner, action: GroupMemberActionPromote, ownerCount: 1, wantErr: true},
		{name: "普通成员可提升", target: member, action: GroupMemberActionPromote, ownerCount: 1},
		{name: "不支持的动作", target: member, action: "transfer", ownerCount: 1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateProjectMemberChange(tc.target, tc.action, tc.ownerCount)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

// TestComputeProjectProgress 覆盖派生进度规则（需求文档 5.3）。
func TestComputeProjectProgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		progresses []int
		statuses   []string
		want       int
	}{
		{name: "无任务时为 0", progresses: nil, statuses: nil, want: 0},
		{name: "单个任务", progresses: []int{50}, statuses: []string{TaskStatusInProgress}, want: 50},
		{name: "简单平均", progresses: []int{50, 100}, statuses: []string{TaskStatusInProgress, TaskStatusCompleted}, want: 75},
		{
			name:       "已取消任务不参与计算",
			progresses: []int{50, 100, 90},
			statuses:   []string{TaskStatusInProgress, TaskStatusCompleted, TaskStatusCancelled},
			want:       75,
		},
		{
			name:       "全部已取消时为 0",
			progresses: []int{90, 80},
			statuses:   []string{TaskStatusCancelled, TaskStatusCancelled},
			want:       0,
		},
		{
			name:       "四舍五入到整数",
			progresses: []int{10, 20, 30},
			statuses:   []string{TaskStatusInProgress, TaskStatusInProgress, TaskStatusInProgress},
			want:       20,
		},
		{
			name:       "状态缺失时仍参与计算",
			progresses: []int{30, 60},
			statuses:   []string{TaskStatusInProgress},
			want:       45,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ComputeProjectProgress(tc.progresses, tc.statuses); got != tc.want {
				t.Fatalf("ComputeProjectProgress = %d，期望 %d", got, tc.want)
			}
		})
	}
}

func TestCountOverdueTasks(t *testing.T) {
	t.Parallel()

	today := MustDate("2026-10-10")

	tests := []struct {
		name     string
		endDates []string
		statuses []string
		want     int
	}{
		{name: "无任务", endDates: nil, statuses: nil, want: 0},
		{
			name:     "统计未完成的逾期任务",
			endDates: []string{"2026-10-05", "2026-10-20"},
			statuses: []string{TaskStatusInProgress, TaskStatusNotStarted},
			want:     1,
		},
		{
			name:     "已完成与已取消不计入",
			endDates: []string{"2026-10-05", "2026-10-05", "2026-10-05"},
			statuses: []string{TaskStatusCompleted, TaskStatusCancelled, TaskStatusInProgress},
			want:     1,
		},
		{name: "今天到期不算逾期", endDates: []string{"2026-10-10"},
			statuses: []string{TaskStatusInProgress}, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			endDates := make([]time.Time, 0, len(tc.endDates))
			for _, raw := range tc.endDates {
				endDates = append(endDates, MustDate(raw))
			}
			if got := CountOverdueTasks(endDates, tc.statuses, today); got != tc.want {
				t.Fatalf("CountOverdueTasks = %d，期望 %d", got, tc.want)
			}
		})
	}
}
