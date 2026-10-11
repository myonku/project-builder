package domain

import (
	"testing"
	"time"
)

// stringPtr / timePtr 是测试用取址辅助。
func stringPtr(value string) *string { return &value }

func timePtr(value time.Time) *time.Time { return &value }

// TestValidateTaskParent 覆盖父子关系规则（需求文档 5.4）。
func TestValidateTaskParent(t *testing.T) {
	t.Parallel()

	projectID := "p1"
	parent := Task{
		ID:        "parent",
		ProjectID: projectID,
		StartDate: MustDate("2026-10-01"),
		EndDate:   MustDate("2026-10-31"),
	}

	tests := []struct {
		name        string
		childID     string
		parent      Task
		parentDepth int
		start, end  string
		projectID   string
		wantErr     bool
	}{
		{
			name: "合法子任务", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-10-05", end: "2026-10-10", projectID: projectID,
		},
		{
			name: "父任务不能是自身", childID: "parent", parent: parent, parentDepth: 1,
			start: "2026-10-05", end: "2026-10-10", projectID: projectID, wantErr: true,
		},
		{
			name: "父任务必须同项目", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-10-05", end: "2026-10-10", projectID: "other", wantErr: true,
		},
		{
			name:    "父任务不能已删除",
			childID: "child",
			parent: func() Task {
				deleted := MustTimestamp("2026-10-01T00:00:00Z")
				copied := parent
				copied.DeletedAt = &deleted
				return copied
			}(),
			parentDepth: 1, start: "2026-10-05", end: "2026-10-10", projectID: projectID, wantErr: true,
		},
		{
			name:    "第 4 层被拒绝（父任务已是第 3 层）",
			childID: "child", parent: parent, parentDepth: TaskMaxDepth,
			start: "2026-10-05", end: "2026-10-10", projectID: projectID, wantErr: true,
		},
		{
			name:    "第 3 层允许（父任务是第 2 层）",
			childID: "child", parent: parent, parentDepth: TaskMaxDepth - 1,
			start: "2026-10-05", end: "2026-10-10", projectID: projectID,
		},
		{
			name: "子任务开始早于父任务", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-09-30", end: "2026-10-10", projectID: projectID, wantErr: true,
		},
		{
			name: "子任务结束晚于父任务", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-10-05", end: "2026-11-01", projectID: projectID, wantErr: true,
		},
		{
			name: "子任务区间倒置", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-10-10", end: "2026-10-05", projectID: projectID, wantErr: true,
		},
		{
			name: "边界与父任务一致", childID: "child", parent: parent, parentDepth: 1,
			start: "2026-10-01", end: "2026-10-31", projectID: projectID,
		},
		{
			// 创建场景：子任务还没有 ID，不应触发"自身"判断。
			name: "childID 为空时跳过自身校验", childID: "", parent: parent, parentDepth: 1,
			start: "2026-10-05", end: "2026-10-10", projectID: projectID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTaskParent(tc.childID, tc.parent, tc.parentDepth,
				MustDate(tc.start), MustDate(tc.end), tc.projectID)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

func TestValidateTaskSelfParent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		taskID    string
		ancestors []string
		wantErr   bool
	}{
		{name: "无祖先", taskID: "t1", ancestors: nil},
		{name: "祖先不含自身", taskID: "t1", ancestors: []string{"p1", "p2"}},
		{name: "祖先含自身表示循环引用", taskID: "t1", ancestors: []string{"p1", "t1"}, wantErr: true},
		{name: "目标是自身", taskID: "t1", ancestors: []string{"t1"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTaskSelfParent(tc.taskID, tc.ancestors)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

func TestTaskDepth(t *testing.T) {
	t.Parallel()

	tests := map[int]int{
		0: 1, // 顶层任务
		1: 2,
		2: 3,
	}
	for ancestorCount, want := range tests {
		if got := TaskDepth(ancestorCount); got != want {
			t.Errorf("TaskDepth(%d) = %d，期望 %d", ancestorCount, got, want)
		}
	}
}

func TestValidateTaskPriority(t *testing.T) {
	t.Parallel()

	for _, priority := range TaskPriorities {
		if err := ValidateTaskPriority(priority); err != nil {
			t.Errorf("ValidateTaskPriority(%d) 期望无错误，实际 %v", priority, err)
		}
	}
	// 数据库列没有 CHECK 约束，取值集合完全由领域层保证。
	for _, priority := range []int{-1, 4, 99, 1000} {
		if err := ValidateTaskPriority(priority); err == nil {
			t.Errorf("ValidateTaskPriority(%d) 期望错误，实际为 nil", priority)
		}
	}
}

// TestTaskPermission 覆盖任务权限矩阵，重点验证归档只读与未共享任务不可见。
func TestTaskPermission(t *testing.T) {
	t.Parallel()

	shared := Task{ID: "t1", ProjectID: "p1", IsShared: true, Status: TaskStatusInProgress}
	unshared := Task{ID: "t2", ProjectID: "p1", IsShared: false, Status: TaskStatusInProgress}

	activeProject := func(visibility string) Project {
		return Project{ID: "p1", Status: ProjectStatusInProgress, Visibility: visibility}
	}
	archivedProject := Project{ID: "p1", Status: ProjectStatusArchived, Visibility: ProjectVisibilityPrivate}

	tests := []struct {
		name              string
		task              Task
		project           Project
		projectPermission Permission
		want              Permission
	}{
		{name: "项目持有者可管理任务", task: unshared,
			project: activeProject(ProjectVisibilityPrivate), projectPermission: PermManage, want: PermManage},
		{name: "项目成员可写任务", task: unshared,
			project: activeProject(ProjectVisibilityPrivate), projectPermission: PermWrite, want: PermWrite},
		{name: "无项目权限则无任务权限", task: shared,
			project: activeProject(ProjectVisibilityGroupEditable), projectPermission: PermNone, want: PermNone},
		{name: "组内可编辑可写已共享任务", task: shared,
			project: activeProject(ProjectVisibilityGroupEditable), projectPermission: PermWrite, want: PermWrite},
		{name: "组内可编辑看不到未共享任务", task: unshared,
			project: activeProject(ProjectVisibilityGroupEditable), projectPermission: PermWrite, want: PermNone},
		{name: "组内只读只能读已共享任务", task: shared,
			project: activeProject(ProjectVisibilityGroupReadOnly), projectPermission: PermRead, want: PermRead},
		{name: "组内只读看不到未共享任务", task: unshared,
			project: activeProject(ProjectVisibilityGroupReadOnly), projectPermission: PermRead, want: PermNone},
		// 归档项目只读：即使持有者也只能读。
		{name: "归档项目持有者降级为只读", task: unshared,
			project: archivedProject, projectPermission: PermManage, want: PermRead},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TaskPermission(tc.task, tc.project, tc.projectPermission); got != tc.want {
				t.Fatalf("TaskPermission = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestValidateTaskMemberInvariant(t *testing.T) {
	t.Parallel()

	// 需求文档 5.4：任务成员必须属于项目成员。
	if err := ValidateTaskMemberInvariant(true); err != nil {
		t.Fatalf("项目成员应被接受，实际 %v", err)
	}
	err := ValidateTaskMemberInvariant(false)
	if err == nil {
		t.Fatal("非项目成员必须被拒绝")
	}
	var domainErr *Error
	if !AsErrorAs(err, &domainErr) {
		t.Fatalf("期望领域错误，实际 %T", err)
	}
	if _, ok := domainErr.Fields["user_id"]; !ok {
		t.Errorf("字段明细缺少 user_id，实际 %v", domainErr.Fields)
	}
}

func TestValidateTaskMemberChange(t *testing.T) {
	t.Parallel()

	owner := TaskMember{UserID: "owner", Role: MemberRoleOwner}
	member := TaskMember{UserID: "member", Role: MemberRoleMember}

	tests := []struct {
		name       string
		target     TaskMember
		action     string
		ownerCount int
		wantErr    bool
	}{
		{name: "唯一 owner 不可移除", target: owner, action: GroupMemberActionRemove, ownerCount: 1, wantErr: true},
		{name: "非唯一 owner 可移除", target: owner, action: GroupMemberActionRemove, ownerCount: 2},
		{name: "唯一 owner 不可降级", target: owner, action: GroupMemberActionDemote, ownerCount: 1, wantErr: true},
		{name: "普通成员不可降级", target: member, action: GroupMemberActionDemote, ownerCount: 1, wantErr: true},
		{name: "普通成员可提升", target: member, action: GroupMemberActionPromote, ownerCount: 1},
		{name: "已是 owner 不可提升", target: owner, action: GroupMemberActionPromote, ownerCount: 1, wantErr: true},
		{name: "不支持的动作", target: member, action: "nope", ownerCount: 1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTaskMemberChange(tc.target, tc.action, tc.ownerCount)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

// TestApplyTaskCopy 覆盖任务复制的基础字段语义。
func TestApplyTaskCopy(t *testing.T) {
	t.Parallel()

	source := Task{
		ID:          "source",
		ProjectID:   "p1",
		ParentID:    stringPtr("parent"),
		CreatedBy:   "alice",
		Name:        "原任务",
		Status:      TaskStatusInProgress,
		Progress:    40,
		Priority:    TaskPriorityHigh,
		StartDate:   MustDate("2026-10-01"),
		EndDate:     MustDate("2026-10-05"),
		IsMilestone: true,
		IsShared:    true,
		DeletedAt:   timePtr(MustTimestamp("2026-10-06T00:00:00Z")),
	}

	t.Run("默认追加副本后缀且清空身份类字段", func(t *testing.T) {
		t.Parallel()
		copied := ApplyTaskCopy(source, TaskCopyInput{})

		if copied.ID != "" {
			t.Errorf("复制的任务不应沿用源 ID，实际 %q", copied.ID)
		}
		if copied.CreatedBy != "" {
			t.Errorf("复制的任务不应沿用源创建者，实际 %q", copied.CreatedBy)
		}
		if copied.DeletedAt != nil {
			t.Errorf("复制的任务不应处于已删除状态")
		}
		// 不保留父子关系：跨父任务复制会破坏"子任务落在父任务区间内"的约束。
		if copied.ParentID != nil {
			t.Errorf("复制不应保留 parent_id，实际 %v", *copied.ParentID)
		}
		if copied.Name != "原任务（副本）" {
			t.Errorf("name = %q，期望追加副本后缀", copied.Name)
		}
		// 其余字段应原样复制。
		if copied.Status != source.Status || copied.Progress != source.Progress ||
			copied.Priority != source.Priority || corrected(copied.IsShared) != source.IsShared {
			t.Errorf("基础字段未被复制: %+v", copied)
		}
	})

	t.Run("名称超长时回退为原名", func(t *testing.T) {
		t.Parallel()
		long := make([]rune, TaskNameMaxLength)
		for i := range long {
			long[i] = '名'
		}
		source := source
		source.Name = string(long)

		copied := ApplyTaskCopy(source, TaskCopyInput{})
		if len([]rune(copied.Name)) > TaskNameMaxLength {
			t.Fatalf("复制后名称长度 = %d，超过上限 %d", len([]rune(copied.Name)), TaskNameMaxLength)
		}
	})

	t.Run("覆盖项生效", func(t *testing.T) {
		t.Parallel()
		newName := "新名称"
		newStart := MustDate("2026-11-01")
		newEnd := MustDate("2026-11-30")
		notShared := false

		copied := ApplyTaskCopy(source, TaskCopyInput{
			Name:      &newName,
			StartDate: &newStart,
			EndDate:   &newEnd,
			IsShared:  &notShared,
		})
		if copied.Name != newName {
			t.Errorf("name = %q，期望 %q", copied.Name, newName)
		}
		if !copied.StartDate.Equal(newStart) || !copied.EndDate.Equal(newEnd) {
			t.Errorf("日期未被覆盖: %v ~ %v", copied.StartDate, copied.EndDate)
		}
		if copied.IsShared {
			t.Error("is_shared 应为 false")
		}
	})
}

// corrected 只是为了让 IsShared 的比较在阅读上更明确。
func corrected(value bool) bool { return value }

func TestIdempotencyScope(t *testing.T) {
	t.Parallel()

	// 作用域必须包含操作与被操作对象，避免跨操作/跨对象串用同一 key
	// （docs/01 5.6）。
	first := IdempotencyScope("tasks.copy", "task-a")
	second := IdempotencyScope("tasks.copy", "task-b")
	third := IdempotencyScope("tasks.move", "task-a")

	if first == second {
		t.Fatal("不同任务的同一操作必须产生不同作用域")
	}
	if first == third {
		t.Fatal("同一任务的不同操作必须产生不同作用域")
	}
	if first != "tasks.copy:task-a" {
		t.Fatalf("作用域格式 = %q，期望 tasks.copy:task-a", first)
	}
}
