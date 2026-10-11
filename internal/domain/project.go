package domain

import (
	"fmt"
	"strings"
	"time"
)

// 字段长度上限，必须与 docs/02-数据库模型与迁移.md 3.5–3.6 的列定义一致。
const (
	ProjectNameMaxLength        = 200
	ProjectDescriptionMaxLength = 4000
	ProjectColorMaxLength       = 32
)

// Project 是项目领域实体。
//
// Progress 与 OverdueTaskCount 是通过查询派生的展示字段，不属于持久化状态
// （接口契约 3.2：查询派生字段）。
type Project struct {
	ID          string
	Name        string
	Description *string
	CreatedBy   string
	GroupID     string
	Status      string
	Visibility  string
	StartDate   time.Time
	EndDate     time.Time
	Color       *string
	ArchivedAt  *time.Time

	Progress         int
	OverdueTaskCount int
}

// IsArchived 报告项目是否已归档。
//
// 归档语义：默认不出现在活动列表中，且项目变为**只读**
// （需求文档 5.3；Python 版本漏掉了只读这一半，实测可继续改任务）。
func (p Project) IsArchived() bool {
	return p.Status == ProjectStatusArchived || p.ArchivedAt != nil
}

// IsReadOnly 报告项目是否处于只读状态。
func (p Project) IsReadOnly() bool { return p.IsArchived() }

// ArchivedError 返回"归档项目只读"的统一错误。
//
// Python 版本在此处有缺陷：`_can_manage_task` 不检查 project.status，
// 导致归档项目仍可被修改。Go 版本把该判断收敛到领域层，用例层必须显式调用它，
// 而不是各自写一句 if 判断。
func ArchivedError() error {
	return ErrConflict.WithField("project_id", "项目已归档，不可修改")
}

// ProjectInput 是创建项目的输入。
type ProjectInput struct {
	Name        string
	Description *string
	GroupID     string
	StartDate   time.Time
	EndDate     time.Time
	Visibility  *string
	Color       *string
}

// NormalizeProjectCreate 清洗创建项目输入。
func NormalizeProjectCreate(in ProjectInput) (ProjectInput, map[string]string) {
	fields := map[string]string{}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		fields["name"] = "项目名称不能为空"
	case len([]rune(in.Name)) > ProjectNameMaxLength:
		fields["name"] = fmt.Sprintf("项目名称不得超过 %d 个字符", ProjectNameMaxLength)
	}

	if strings.TrimSpace(in.GroupID) == "" {
		fields["group_id"] = "必须指定项目归属工作组"
	} else {
		in.GroupID = strings.TrimSpace(in.GroupID)
	}

	// 需求文档 5.3：开始日期不能晚于结束日期。
	// 该错误必须保留为 invalid_date_range（400），不能并入 422 字段集合。
	dateErr := ValidateDateRange(in.StartDate, in.EndDate)
	MergeErrorFields(dateErr, fields)

	in.Description = normalizeOptionalText(in.Description, ProjectDescriptionMaxLength, "description", fields)

	if in.Visibility != nil {
		visibility := strings.TrimSpace(*in.Visibility)
		if !containsString(ProjectVisibilities, visibility) {
			fields["visibility"] = fmt.Sprintf("共享级别只能是 %s 之一",
				strings.Join(ProjectVisibilities, "、"))
		} else {
			in.Visibility = &visibility
		}
	}

	if in.Color != nil {
		color := strings.TrimSpace(*in.Color)
		switch {
		case color == "":
			in.Color = nil
		case len([]rune(color)) > ProjectColorMaxLength:
			fields["color"] = fmt.Sprintf("颜色值不得超过 %d 个字符", ProjectColorMaxLength)
		default:
			in.Color = &color
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// ProjectDateRangeError 返回 ProjectInput 中潜在的日期区间错误。
//
// 单独暴露的原因：NormalizeProjectCreate 只返回 fields 映射，会丢失错误码。
// 调用方需要据此把"日期区间倒置"映射为契约规定的 400 invalid_date_range，
// 而不是笼统的 422。判断逻辑集中在领域层，避免各调用点各写一遍。
func ProjectDateRangeError(start, end time.Time) *Error {
	var err *Error
	if AsErrorAs(ValidateDateRange(start, end), &err) && err.Code == CodeInvalidDateRange {
		return err
	}
	return nil
}

// ProjectUpdateInput 是修改项目的部分字段。nil 表示不修改。
type ProjectUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
	Visibility  *string
	StartDate   *time.Time
	EndDate     *time.Time
	Color       *string
}

// NormalizeProjectUpdate 清洗修改项目的输入。
func NormalizeProjectUpdate(in ProjectUpdateInput) (ProjectUpdateInput, map[string]string) {
	fields := map[string]string{}

	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		switch {
		case trimmed == "":
			fields["name"] = "项目名称不能为空"
		case len([]rune(trimmed)) > ProjectNameMaxLength:
			fields["name"] = fmt.Sprintf("项目名称不得超过 %d 个字符", ProjectNameMaxLength)
		default:
			in.Name = &trimmed
		}
	}

	if in.Description != nil {
		trimmed := strings.TrimSpace(*in.Description)
		if len([]rune(trimmed)) > ProjectDescriptionMaxLength {
			fields["description"] = fmt.Sprintf("描述不得超过 %d 个字符", ProjectDescriptionMaxLength)
		} else {
			in.Description = &trimmed
		}
	}

	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if !containsString(ProjectStatuses, status) {
			fields["status"] = fmt.Sprintf("状态只能是 %s 之一", strings.Join(ProjectStatuses, "、"))
		} else {
			in.Status = &status
		}
	}

	if in.Visibility != nil {
		visibility := strings.TrimSpace(*in.Visibility)
		if !containsString(ProjectVisibilities, visibility) {
			fields["visibility"] = fmt.Sprintf("共享级别只能是 %s 之一",
				strings.Join(ProjectVisibilities, "、"))
		} else {
			in.Visibility = &visibility
		}
	}

	if in.Color != nil {
		color := strings.TrimSpace(*in.Color)
		if len([]rune(color)) > ProjectColorMaxLength {
			fields["color"] = fmt.Sprintf("颜色值不得超过 %d 个字符", ProjectColorMaxLength)
		} else {
			in.Color = &color
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// ValidateProjectDateRange 在部分更新场景下校验合并后的日期区间。
//
// 必须用"合并后的最终值"校验：只改 end_date 时，若拿请求体里的 start_date（为空）
// 去比较就会漏掉冲突。
func ValidateProjectDateRange(start, end time.Time) error {
	return ValidateDateRange(start, end)
}

// ProjectMember 是项目成员关系。
type ProjectMember struct {
	ID        string
	ProjectID string
	UserID    string
	Role      string
}

// IsOwner 报告成员是否为项目持有者。
func (m ProjectMember) IsOwner() bool { return m.Role == MemberRoleOwner }

// ProjectPermission 计算 actor 对 project 的权限。
//
// 需求文档 4.2 / 4.3：
//   - 默认私有，明确共享后才进入组内视图；
//   - "组内可见"不等于"可编辑"；
//   - 项目共享级别本身不授予修改项目（含共享权限）的能力。
//
// 需求文档 4.1：系统管理员"管理所有用户、工作组和系统级数据，处理异常数据"。
// 因此**系统管理员对所有项目都有管理权限**，且不要求他是项目成员或组员。
// 该放行必须放在最前面：管理员是异常情况下的兜底手段
// （例如项目持有者已离职/被停用），若先被成员或组状态挡掉，这个兜底就失效了。
//
// 放行的**边界**（同样重要）：
//   - 只豁免"权限判定"，不豁免状态规则与不变式。归档项目对管理员同样是只读的
//     （见 TaskPermission 与 Project.IsReadOnly），唯一持有者/唯一管理员等不变式
//     对管理员一样生效。
//   - 由用例层传入的 actor 必须携带真实的 SystemRole。早期实现各处传
//     domain.User{ID: actorID}，角色恒为空，于是"管理员放行"写了却没生效——
//     本仓库把这称为同一类缺陷，见 docs/06-迁移进度与验证.md 5.12。
func ProjectPermission(project Project, actor User, member *ProjectMember, group Group, groupMembership *GroupMember) Permission {
	if actor.IsAdmin() {
		return PermManage
	}

	// 项目成员：持有者可管理，普通成员可写。
	if member != nil {
		if member.IsOwner() {
			return PermManage
		}
		return PermWrite
	}

	// 非项目成员：只能通过组内共享获得权限，且组必须可用。
	if !CanAccessGroup(group, groupMembership) {
		return PermNone
	}

	switch project.Visibility {
	case ProjectVisibilityGroupEditable:
		return PermWrite
	case ProjectVisibilityGroupReadOnly:
		return PermRead
	default:
		// private：非成员不可见。
		return PermNone
	}
}

// CanManageProject 报告 actor 能否修改项目本身（名称、日期、共享级别、成员）。
//
// 需求文档 4.2 第 4 条：项目创建者默认为项目持有者，并拥有项目管理权限。
// 需求文档 4.3：组内可编辑允许修改共享任务，但**不能**修改项目共享权限。
func CanManageProject(project Project, actor User, member *ProjectMember) bool {
	// 需求文档 4.1：系统管理员管理所有项目（处理异常数据），不要求是成员。
	if actor.IsAdmin() {
		return true
	}
	return member != nil && member.IsOwner()
}

// CanEditProjectTasks 报告 actor 能否修改该项目下的任务。
//
// 归档项目只读：无论什么权限都不可编辑（Python 版本此处有缺陷）。
func CanEditProjectTasks(project Project, permission Permission) bool {
	if project.IsReadOnly() {
		return false
	}
	return permission.AtLeast(PermWrite)
}

// ValidateProjectMemberChange 校验项目成员变更。
//
// 核心不变式：一个项目最多一个 owner，且**不得移除唯一 owner**
// （接口契约 4.5：不得移除唯一 owner）。数据库的部分唯一索引保证"最多一个"，
// "不得移除最后一个"必须在这里判断。
//
// 关于"设为持有者"：因为持有者唯一，"设为持有者"只可能是**转移**，
// 由 GroupMemberActionTransfer 表达；这里刻意不再接受 Promote——
// 早期版本把 role=owner 映射成 Promote，而 Promote 又在 ownerCount >= 1 时被拒绝，
// 结果"持有者永远无法变更"，而错误文案却让用户"先转移持有者"，实际没有任何路径。
func ValidateProjectMemberChange(target ProjectMember, action string, ownerCount int) error {
	switch action {
	case GroupMemberActionRemove:
		if target.IsOwner() && ownerCount <= 1 {
			return ErrPermissionDenied.WithField("user_id", "不能移除项目的唯一持有者")
		}
	case GroupMemberActionTransfer:
		if target.IsOwner() {
			return ErrConflict.WithField("role", "该成员已经是项目持有者")
		}
	case GroupMemberActionDemote:
		if !target.IsOwner() {
			return ErrConflict.WithField("role", "该成员不是项目持有者")
		}
		if ownerCount <= 1 {
			return ErrPermissionDenied.WithField("user_id", "不能降级项目的唯一持有者；如需换人请直接转移持有者")
		}
	default:
		return ErrInvalidRequest.WithField("action", "不支持的成员变更动作")
	}
	return nil
}

// ComputeProjectProgress 计算项目进度。
//
// 需求文档 5.3：项目进度为有效任务进度的简单平均值；没有有效任务时显示 0%；
// 已取消任务不参与计算。已逻辑删除的任务同样不参与。
func ComputeProjectProgress(progresses []int, statuses []string) int {
	total := 0
	count := 0
	for i, progress := range progresses {
		status := ""
		if i < len(statuses) {
			status = statuses[i]
		}
		if status == TaskStatusCancelled {
			continue
		}
		total += progress
		count++
	}
	if count == 0 {
		return 0
	}
	// 简单平均值，四舍五入到整数百分比。
	return (total + count/2) / count
}

// CountOverdueTasks 统计逾期任务数。
//
// 逾期是派生字段：end_date < 今天 且状态未结束。
// 已取消与已完成任务不计入（IsOverdue 已排除终态）。
func CountOverdueTasks(endDates []time.Time, statuses []string, today time.Time) int {
	count := 0
	for i, endDate := range endDates {
		status := ""
		if i < len(statuses) {
			status = statuses[i]
		}
		if IsOverdue(endDate, status, today) {
			count++
		}
	}
	return count
}
