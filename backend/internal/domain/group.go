package domain

import (
	"fmt"
	"strings"
)

// 字段长度上限，必须与 backend/docs/B02-数据库模型与迁移.md 3.2–3.4 的列定义一致。
const (
	GroupNameMaxLength        = 160
	GroupDescriptionMaxLength = 2000
)

// Group 是工作组领域实体。
type Group struct {
	ID          string
	Name        string
	Description *string
	Status      string
	OwnerID     string
}

// IsActive 报告工作组是否可用。停用工作组的数据不可访问（见 CanAccessGroup）。
func (g Group) IsActive() bool { return g.Status == GroupStatusActive }

// GroupMember 是工作组成员关系。
type GroupMember struct {
	ID      string
	GroupID string
	UserID  string
	Role    string
	Status  string
}

// IsActive 报告成员关系是否有效。
func (m GroupMember) IsActive() bool { return m.Status == MemberStatusActive }

// IsManager 报告成员是否为组管理员。
func (m GroupMember) IsManager() bool {
	return m.Status == MemberStatusActive && m.Role == MemberRoleManager
}

// GroupMembershipInput 是创建工作组时的初始参数。
type GroupMembershipInput struct {
	Name        string
	Description *string
}

// NormalizeGroupCreate 清洗创建工作组输入。
func NormalizeGroupCreate(in GroupMembershipInput) (GroupMembershipInput, map[string]string) {
	fields := map[string]string{}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		fields["name"] = "工作组名称不能为空"
	case len([]rune(in.Name)) > GroupNameMaxLength:
		fields["name"] = fmt.Sprintf("工作组名称不得超过 %d 个字符", GroupNameMaxLength)
	}

	in.Description = normalizeOptionalText(in.Description, GroupDescriptionMaxLength, "description", fields)

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// GroupUpdateInput 是修改工作组的部分字段。nil 表示不修改。
type GroupUpdateInput struct {
	Name        *string
	Description *string
	Status      *string
}

// NormalizeGroupUpdate 清洗修改工作组的输入。
func NormalizeGroupUpdate(in GroupUpdateInput) (GroupUpdateInput, map[string]string) {
	fields := map[string]string{}

	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		switch {
		case trimmed == "":
			fields["name"] = "工作组名称不能为空"
		case len([]rune(trimmed)) > GroupNameMaxLength:
			fields["name"] = fmt.Sprintf("工作组名称不得超过 %d 个字符", GroupNameMaxLength)
		default:
			in.Name = &trimmed
		}
	}

	if in.Description != nil {
		trimmed := strings.TrimSpace(*in.Description)
		if len([]rune(trimmed)) > GroupDescriptionMaxLength {
			fields["description"] = fmt.Sprintf("描述不得超过 %d 个字符", GroupDescriptionMaxLength)
		} else {
			in.Description = &trimmed
		}
	}

	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if !containsString([]string{GroupStatusActive, GroupStatusDisabled}, status) {
			fields["status"] = "状态只能是 active 或 disabled"
		} else {
			in.Status = &status
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// CanAccessGroup 报告用户能否访问工作组数据。
//
// 需求文档 5.2：审核通过前不能访问组内计划；组被停用后成员也不能访问。
// 注意：OwnerID 不构成访问豁免——所有者若被移除成员关系，也就不再是成员。
// 该规则由"所有者成员关系不可降级"保证（见 backend/docs/B02 §3.2.1）。
func CanAccessGroup(group Group, membership *GroupMember) bool {
	if !group.IsActive() {
		return false
	}
	return membership != nil && membership.IsActive()
}

// CanManageGroupMembers 报告用户能否管理组成员（审核申请、移除成员、设置角色）。
//
// 需求文档 5.2：工作组所有者可以审核加入申请、移除成员及设置组管理员；
// 系统管理员"管理所有用户、工作组和系统级数据"（需求文档 4.1），因此同样放行。
//
// 管理员的判断必须在**组是否可用之前**：否则一个被停用的工作组连管理员都改不了，
// 也就没人能把它重新启用——这正是"管理员兜底"要避免的死锁。
func CanManageGroupMembers(group Group, actor User, membership *GroupMember) bool {
	if actor.IsAdmin() {
		return true
	}
	if !group.IsActive() {
		return false
	}
	if group.OwnerID == actor.ID {
		return true
	}
	return membership != nil && membership.IsManager()
}

// CanViewGroup 报告 actor 能否查看该工作组的详情与成员列表。
//
// 与 requireAccess 的既有语义完全一致（所有者或活跃成员），只额外放行系统管理员。
// 刻意**不**检查组是否停用：停用的组对原成员仍然可见（他们需要看到"组已停用"），
// 这一行为在引入管理员放行时保持不变。
func CanViewGroup(group Group, actor User, membership *GroupMember) bool {
	if actor.IsAdmin() {
		return true
	}
	if group.OwnerID == actor.ID {
		return true
	}
	return membership != nil && membership.IsActive()
}

// CanTransferGroupOwnership 报告 actor 能否转移工作组所有权。
//
// 只有当前所有者与系统管理员可以。组管理员（manager）**不能**：
// 把所有权交给谁是治理决定，不是日常成员管理；REST 语义上它更接近"改组的属性"，
// 因此走 PATCH /groups/{id} 的 owner_id 字段而不是成员角色接口。
func CanTransferGroupOwnership(group Group, actor User) bool {
	if actor.IsAdmin() {
		return true
	}
	return group.OwnerID != "" && group.OwnerID == actor.ID
}

// CanUpdateGroup 报告用户能否修改组本身（名称、描述、状态、所有者）。
//
// 需求文档 5.2：系统管理员可以修改和停用工作组。
func CanUpdateGroup(group Group, actor User, membership *GroupMember) bool {
	return CanManageGroupMembers(group, actor, membership)
}

// 成员变更动作。
const (
	GroupMemberActionPromote = "promote" // 设为组管理员
	GroupMemberActionDemote  = "demote"  // 降为普通成员
	GroupMemberActionRemove  = "remove"  // 移除成员
	GroupMemberActionRestore = "restore" // 恢复被移除的成员
	// GroupMemberActionTransfer 把**唯一持有者**身份转移给该成员。
	//
	// 它与 Promote 的区别是"一个还是可能多个"：组管理员可以有多个，
	// 而项目/任务的持有者是唯一的（数据库部分唯一索引保证）。
	// 因此"设为持有者"必然意味着"从现任持有者手里接管"，
	// 必须在同一事务内先降后升，而不是新增第二个持有者。
	GroupMemberActionTransfer = "transfer"
)

// ValidateGroupMemberChange 校验一次成员变更是否被允许。
//
// 核心不变式（backend/docs/B02 §3.2.1 保障 2）：
// 工作组所有者对应的成员关系不得被降级或移除。所有者身份由 groups.owner_id 表达，
// 成员行只是它的投影；允许降级会让两者静默不一致。
func ValidateGroupMemberChange(group Group, target GroupMember, action string) error {
	if target.UserID == group.OwnerID {
		switch action {
		case GroupMemberActionDemote, GroupMemberActionRemove:
			return ErrPermissionDenied.WithField("user_id",
				"不能降级或移除工作组所有者")
		}
	}

	switch action {
	case GroupMemberActionPromote:
		if target.Role == MemberRoleManager {
			return ErrConflict.WithField("role", "该成员已经是组管理员")
		}
	case GroupMemberActionDemote:
		if target.Role != MemberRoleManager {
			return ErrConflict.WithField("role", "该成员不是组管理员")
		}
	case GroupMemberActionRemove:
		if target.Status == MemberStatusRemoved {
			return ErrConflict.WithField("status", "该成员已被移除")
		}
	case GroupMemberActionRestore:
		if target.Status == MemberStatusActive {
			return ErrConflict.WithField("status", "该成员已是活跃成员")
		}
	default:
		return ErrInvalidRequest.WithField("action", "不支持的成员变更动作")
	}
	return nil
}

// GroupJoinRequest 是加入申请。
type GroupJoinRequest struct {
	ID         string
	GroupID    string
	UserID     string
	Status     string
	ReviewedBy *string
	ReviewedAt *string
}

// IsPending 报告申请是否仍在等待审核。
func (r GroupJoinRequest) IsPending() bool { return r.Status == JoinRequestPending }

// ValidateJoinRequestDecision 校验审核决定。
//
// 需求文档 5.2 + 服务端设计：申请只审核一次，且不能回到 pending。
// decided 为当前的申请状态，decision 为本次目标状态。
func ValidateJoinRequestDecision(current GroupJoinRequest, decision string) error {
	if decision != JoinRequestApproved && decision != JoinRequestRejected {
		return ErrValidation.WithField("status", "审核结果只能是 approved 或 rejected")
	}
	if !current.IsPending() {
		return ErrConflict.WithField("status",
			fmt.Sprintf("申请已处于 %s 状态，不能重复审核", current.Status))
	}
	return nil
}

// 加入申请在"待审核"状态下的唯一性。
//
// 数据库层有 group_join_request_identity UNIQUE(group_id, user_id) 阻止重复申请，
// 但被拒绝后用户应能重新申请。因此这里给出明确的冲突语义，
// 由仓储在插入冲突时翻译（见 app/groups/repo.go）。
func JoinRequestConflictMessage(current GroupJoinRequest) string {
	switch current.Status {
	case JoinRequestPending:
		return "已存在待审核的加入申请"
	case JoinRequestApproved:
		return "已是该工作组成员"
	default:
		return "加入申请状态冲突"
	}
}

// normalizeOptionalText 清洗可选文本字段，返回 nil 表示"未提供/置空"。
func normalizeOptionalText(value *string, maxLength int, fieldName string, fields map[string]string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	if len([]rune(trimmed)) > maxLength {
		fields[fieldName] = fmt.Sprintf("长度不得超过 %d 个字符", maxLength)
		return nil
	}
	return &trimmed
}

// containsString 报告 values 是否包含 target。
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
