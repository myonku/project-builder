package domain

import (
	"fmt"
	"strings"
	"time"
)

// 字段长度上限，必须与 docs/02-数据库模型与迁移.md 3.7–3.8 的列定义一致。
const (
	TaskNameMaxLength        = 200
	TaskDescriptionMaxLength = 4000
)

// 优先级稳定机器值。
//
// 需求文档 5.4 描述为"低、普通、高、紧急"。数据库列是 INTEGER 且没有 CHECK 约束，
// 因此取值范围**只能由领域层保证**——这正是需要在文档与测试中显式固定的原因。
const (
	TaskPriorityLow    = 0
	TaskPriorityNormal = 1
	TaskPriorityHigh   = 2
	TaskPriorityUrgent = 3
)

// TaskPriorities 是允许的优先级取值。
var TaskPriorities = []int{TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh, TaskPriorityUrgent}

// ValidateTaskPriority 校验优先级取值。
func ValidateTaskPriority(priority int) error {
	for _, allowed := range TaskPriorities {
		if priority == allowed {
			return nil
		}
	}
	return ErrValidation.WithField("priority",
		fmt.Sprintf("优先级只能是 %d（低）、%d（普通）、%d（高）、%d（紧急）之一",
			TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh, TaskPriorityUrgent))
}

// Task 是任务领域实体。
type Task struct {
	ID          string
	ProjectID   string
	ParentID    *string
	CreatedBy   string
	Name        string
	Description *string
	Status      string
	Progress    int
	Priority    int
	StartDate   time.Time
	EndDate     time.Time
	IsMilestone bool
	IsShared    bool
	DeletedAt   *time.Time
}

// IsDeleted 报告任务是否已被逻辑删除。
func (t Task) IsDeleted() bool { return t.DeletedAt != nil }

// IsTerminal 报告任务是否处于终态（已完成或已取消）。
func (t Task) IsTerminal() bool {
	return t.Status == TaskStatusCompleted || t.Status == TaskStatusCancelled
}

// TaskInput 是创建任务的输入。
type TaskInput struct {
	Name        string
	Description *string
	ParentID    *string
	StartDate   time.Time
	EndDate     time.Time
	Priority    *int
	IsMilestone *bool
	IsShared    *bool
}

// NormalizeTaskCreate 清洗创建任务输入。
func NormalizeTaskCreate(in TaskInput) (TaskInput, map[string]string) {
	fields := map[string]string{}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		fields["name"] = "任务名称不能为空"
	case len([]rune(in.Name)) > TaskNameMaxLength:
		fields["name"] = fmt.Sprintf("任务名称不得超过 %d 个字符", TaskNameMaxLength)
	}

	if err := ValidateDateRange(in.StartDate, in.EndDate); err != nil {
		MergeErrorFields(err, fields)
	}

	in.Description = normalizeOptionalText(in.Description, TaskDescriptionMaxLength, "description", fields)

	if in.ParentID != nil {
		trimmed := strings.TrimSpace(*in.ParentID)
		if trimmed == "" {
			in.ParentID = nil
		} else {
			in.ParentID = &trimmed
		}
	}

	if in.Priority != nil {
		if err := ValidateTaskPriority(*in.Priority); err != nil {
			MergeErrorFields(err, fields)
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// TaskUpdateInput 是修改任务的部分字段。nil 表示不修改。
type TaskUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
	Progress    *int
	Priority    *int
	StartDate   *time.Time
	EndDate     *time.Time
	IsMilestone *bool
	IsShared    *bool
	ParentID    *string
	// ParentIDSet 区分"未提供 parent_id"与"显式置空 parent_id"。
	// JSON 的 null 与省略无法用 *string 区分，因此由 HTTP 层用是否为 null 显式设置。
	ParentIDSet bool
}

// NormalizeTaskUpdate 清洗修改任务的输入。
func NormalizeTaskUpdate(in TaskUpdateInput) (TaskUpdateInput, map[string]string) {
	fields := map[string]string{}

	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		switch {
		case trimmed == "":
			fields["name"] = "任务名称不能为空"
		case len([]rune(trimmed)) > TaskNameMaxLength:
			fields["name"] = fmt.Sprintf("任务名称不得超过 %d 个字符", TaskNameMaxLength)
		default:
			in.Name = &trimmed
		}
	}

	if in.Description != nil {
		trimmed := strings.TrimSpace(*in.Description)
		if len([]rune(trimmed)) > TaskDescriptionMaxLength {
			fields["description"] = fmt.Sprintf("描述不得超过 %d 个字符", TaskDescriptionMaxLength)
		} else {
			in.Description = &trimmed
		}
	}

	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if !containsString(TaskStatuses, status) {
			fields["status"] = fmt.Sprintf("状态只能是 %s 之一", strings.Join(TaskStatuses, "、"))
		} else {
			in.Status = &status
		}
	}

	if in.Progress != nil {
		if err := ValidateProgress(*in.Progress); err != nil {
			MergeErrorFields(err, fields)
		}
	}

	if in.Priority != nil {
		if err := ValidateTaskPriority(*in.Priority); err != nil {
			MergeErrorFields(err, fields)
		}
	}

	if in.ParentID != nil {
		trimmed := strings.TrimSpace(*in.ParentID)
		if trimmed == "" {
			in.ParentID = nil
		} else {
			in.ParentID = &trimmed
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// TaskMember 是任务成员关系。
type TaskMember struct {
	ID     string
	TaskID string
	UserID string
	Role   string
}

// IsOwner 报告成员是否为任务持有者。
func (m TaskMember) IsOwner() bool { return m.Role == MemberRoleOwner }

// ValidateTaskParent 校验父子关系的合法性。
//
// 规则（需求文档 5.4）：
//   - 首期支持父子任务，最大层级限制为 3 层；
//   - 子任务必须与父任务属于同一项目；
//   - 父任务时间范围应覆盖子任务。
//
// 参数：
//   - childID：子任务自身的 ID（用于拒绝自我引用）；
//   - parent：目标父任务；
//   - parentDepth：父任务自身的层级（顶层任务为 1）；
//   - childStart/childEnd：子任务的日期区间；
//   - projectID：子任务所属项目。
func ValidateTaskParent(childID string, parent Task, parentDepth int, childStart, childEnd time.Time, projectID string) error {
	if childID != "" && parent.ID == childID {
		return ErrInvalidRequest.WithField("parent_id", "父任务不能是自身")
	}
	if parent.ProjectID != projectID {
		return ErrInvalidRequest.WithField("parent_id", "父任务必须与子任务属于同一项目")
	}
	if parent.IsDeleted() {
		return ErrInvalidRequest.WithField("parent_id", "父任务已被删除")
	}

	// 父任务深度为 parentDepth，子任务深度为 parentDepth+1，不得超过 TaskMaxDepth。
	if parentDepth+1 > TaskMaxDepth {
		return ErrValidation.WithField("parent_id",
			fmt.Sprintf("任务层级不得超过 %d 层", TaskMaxDepth))
	}

	if err := ValidateDateRange(childStart, childEnd); err != nil {
		return err
	}
	// 父任务时间范围应覆盖子任务。
	if DayOf(childStart).Before(DayOf(parent.StartDate)) || DayOf(childEnd).After(DayOf(parent.EndDate)) {
		return ErrValidation.WithField("start_date",
			"子任务的日期区间必须落在父任务的日期区间内")
	}
	return nil
}

// ValidateTaskSelfParent 校验任务不能成为自身的父任务（含循环引用）。
//
// ancestors 是从目标父任务向上追溯到的全部祖先 ID。
func ValidateTaskSelfParent(taskID string, ancestors []string) error {
	for _, ancestorID := range ancestors {
		if ancestorID == taskID {
			return ErrValidation.WithField("parent_id", "父任务不能是任务的自身或其子孙")
		}
	}
	return nil
}

// TaskDepth 计算给定祖先链长度对应的层级。顶层任务为 1。
func TaskDepth(ancestorCount int) int { return ancestorCount + 1 }

// TaskPermission 计算 actor 对 task 的权限。
//
// 任务权限建立在项目权限之上：
//   - 项目持有者按项目级 PermManage 获得任务的管理权限；
//   - 项目成员（非持有者）获得读写权限；
//   - 组内共享只影响"是否能看到共享任务"：组内只读 → 仅读已共享任务；
//     组内可编辑 → 可读写已共享任务；未共享任务一律不可见；
//   - 归档项目一律只读。
func TaskPermission(task Task, project Project, projectPermission Permission) Permission {
	if projectPermission == PermNone {
		return PermNone
	}

	// 归档项目只读：即使有写权限也只给读。
	if project.IsReadOnly() {
		return PermRead
	}

	// 项目成员：按项目内角色决定。
	if projectPermission.AtLeast(PermManage) {
		return PermManage
	}
	if projectPermission.AtLeast(PermWrite) {
		// 需要区分"项目成员"与"组内可编辑"两种来源：
		// 后者只能操作已共享任务。
		if project.Visibility == ProjectVisibilityGroupEditable && !task.IsShared {
			return PermNone
		}
		return PermWrite
	}

	// 仅读权限：组内只读只能看到已共享任务。
	if task.IsShared {
		return PermRead
	}
	return PermNone
}

// ValidateTaskMemberInvariant 校验任务成员不变式。
//
// 需求文档 5.4：任务成员必须属于项目成员，持有者也必须是项目成员。
// 返回 isProjectMember 为 false 时表示目标用户不是项目成员。
func ValidateTaskMemberInvariant(isProjectMember bool) error {
	if !isProjectMember {
		return ErrValidation.WithField("user_id", "任务成员必须是项目成员")
	}
	return nil
}

// ValidateTaskMemberChange 校验任务成员变更。
//
// 与项目一致：最多一个 owner，且不得移除/降级唯一 owner。
func ValidateTaskMemberChange(target TaskMember, action string, ownerCount int) error {
	switch action {
	case GroupMemberActionRemove:
		if target.IsOwner() && ownerCount <= 1 {
			return ErrPermissionDenied.WithField("user_id", "不能移除任务的唯一持有者")
		}
	case GroupMemberActionPromote:
		if target.IsOwner() {
			return ErrConflict.WithField("role", "该成员已经是任务持有者")
		}
	case GroupMemberActionDemote:
		if !target.IsOwner() {
			return ErrConflict.WithField("role", "该成员不是任务持有者")
		}
		if ownerCount <= 1 {
			return ErrPermissionDenied.WithField("user_id", "不能降级任务的唯一持有者")
		}
	default:
		return ErrInvalidRequest.WithField("action", "不支持的成员变更动作")
	}
	return nil
}

// TaskCopyInput 描述复制任务时要覆盖的字段。
//
// 需求文档 5.4：首期复制任务可以简化为复制基础字段，不复制提醒发送记录。
// 复制出的任务同样必须满足日期与项目约束，因此这里显式要求新的日期。
type TaskCopyInput struct {
	Name        *string
	Description *string
	StartDate   *time.Time
	EndDate     *time.Time
	IsShared    *bool
}

// NormalizeTaskCopy 清洗复制任务输入。所有字段可选，未提供时沿用源任务。
func NormalizeTaskCopy(in TaskCopyInput) (TaskCopyInput, map[string]string) {
	fields := map[string]string{}

	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		switch {
		case trimmed == "":
			fields["name"] = "任务名称不能为空"
		case len([]rune(trimmed)) > TaskNameMaxLength:
			fields["name"] = fmt.Sprintf("任务名称不得超过 %d 个字符", TaskNameMaxLength)
		default:
			in.Name = &trimmed
		}
	}

	if in.Description != nil {
		trimmed := strings.TrimSpace(*in.Description)
		if len([]rune(trimmed)) > TaskDescriptionMaxLength {
			fields["description"] = fmt.Sprintf("描述不得超过 %d 个字符", TaskDescriptionMaxLength)
		} else {
			in.Description = &trimmed
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// ApplyTaskCopy 把复制覆盖项应用到源任务，返回新任务的字段。
func ApplyTaskCopy(source Task, in TaskCopyInput) Task {
	copied := source
	copied.ID = ""
	copied.CreatedBy = ""
	copied.DeletedAt = nil
	copied.ParentID = nil

	if in.Name != nil {
		copied.Name = *in.Name
	} else {
		copied.Name = source.Name + "（副本）"
		// 名称长度上限仍需满足。
		if len([]rune(copied.Name)) > TaskNameMaxLength {
			copied.Name = source.Name
		}
	}
	if in.Description != nil {
		copied.Description = in.Description
	}
	if in.StartDate != nil {
		copied.StartDate = *in.StartDate
	}
	if in.EndDate != nil {
		copied.EndDate = *in.EndDate
	}
	if in.IsShared != nil {
		copied.IsShared = *in.IsShared
	}
	return copied
}

// IdempotencyScope 返回某个操作使用的幂等键作用域。
//
// 作用域必须包含操作语义，避免跨操作串用同一 key（docs/01 5.6）。
// 形如 tasks.copy:<task_id>，使同一 key 在不同任务上互不冲突。
func IdempotencyScope(operation, resourceID string) string {
	return operation + ":" + resourceID
}
