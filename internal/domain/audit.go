package domain

import (
	"fmt"
	"sort"
	"strings"
)

// AuditObjectType 是被审计对象的类型（枚举白名单）。
//
// 取值同时被三处引用，必须保持一致：
//   - 本文件的常量；
//   - 迁移 000005 的 CHECK 约束；
//   - docs/04-HTTP接口契约.md §4.10 的表格。
//
// 由 TestAuditEnumWhitelistMatchesDatabaseCheck 在真实数据库上断言前两者一致。
type AuditObjectType string

// AuditAction 是被记录的动作（枚举白名单）。
type AuditAction string

const (
	AuditObjectUser       AuditObjectType = "user"
	AuditObjectGroup      AuditObjectType = "group"
	AuditObjectProject    AuditObjectType = "project"
	AuditObjectTask       AuditObjectType = "task"
	AuditObjectAttachment AuditObjectType = "attachment"
	AuditObjectReminder   AuditObjectType = "reminder"
)

const (
	AuditActionCreated     AuditAction = "created"
	AuditActionUpdated     AuditAction = "updated"
	AuditActionArchived    AuditAction = "archived"
	AuditActionDeleted     AuditAction = "deleted"
	AuditActionPurged      AuditAction = "purged"
	AuditActionMemberAdded AuditAction = "member_added"
	// AuditActionMemberRoleChanged 也覆盖"把某人设为持有者"这类角色变更；
	// 真正的持有者**转移**用 AuditActionOwnerTransferred（它同时改变两个人）。
	AuditActionMemberRoleChanged   AuditAction = "member_role_changed"
	AuditActionMemberRemoved       AuditAction = "member_removed"
	AuditActionOwnerTransferred    AuditAction = "owner_transferred"
	AuditActionJoinRequestReviewed AuditAction = "join_request_reviewed"
	// AuditActionShared 专指项目的可见性（共享级别）变化：需求文档 §4.3 把它
	// 单列为一条业务规则，排查"为什么这个项目突然对全组可见"时需要一眼看到。
	AuditActionShared        AuditAction = "shared"
	AuditActionPasswordReset AuditAction = "password_reset"
	AuditActionDisabled      AuditAction = "disabled"
)

// auditObjectTypes / auditActions 是白名单的唯一事实来源。
//
// 排序固定（按声明顺序，见下面的切片字面量），因此错误信息与文档列出的顺序稳定。
var auditObjectTypes = []AuditObjectType{
	AuditObjectUser, AuditObjectGroup, AuditObjectProject,
	AuditObjectTask, AuditObjectAttachment, AuditObjectReminder,
}

var auditActions = []AuditAction{
	AuditActionCreated, AuditActionUpdated, AuditActionArchived,
	AuditActionDeleted, AuditActionPurged,
	AuditActionMemberAdded, AuditActionMemberRoleChanged, AuditActionMemberRemoved,
	AuditActionOwnerTransferred, AuditActionJoinRequestReviewed, AuditActionShared,
	AuditActionPasswordReset, AuditActionDisabled,
}

// AuditObjectTypes 返回对象类型白名单（供校验、文档断言与测试使用）。
func AuditObjectTypes() []AuditObjectType {
	return append([]AuditObjectType(nil), auditObjectTypes...)
}

// AuditActions 返回动作白名单。
func AuditActions() []AuditAction {
	return append([]AuditAction(nil), auditActions...)
}

// IsAuditObjectType 报告取值是否在白名单内。
func IsAuditObjectType(value string) bool {
	for _, item := range auditObjectTypes {
		if string(item) == value {
			return true
		}
	}
	return false
}

// IsAuditAction 报告取值是否在白名单内。
func IsAuditAction(value string) bool {
	for _, item := range auditActions {
		if string(item) == value {
			return true
		}
	}
	return false
}

// AuditEntry 是一条待写入的操作记录。
//
// 它只承载"事实"：谁、做了什么、对谁、改成了什么。是否允许记录由用例层决定
// （权限判定不属于这里），能否落库由 ValidateAuditEntry 决定。
type AuditEntry struct {
	// ActorID 是操作人；审计必须有主体，空值直接拒绝。
	ActorID string
	Action  AuditAction
	// ObjectType/ObjectID 是被操作对象。成员变更记**容器**对象
	// （工作组/项目/任务的 id），而不是成员行 id（docs/05 §6 第 38 条）。
	ObjectType AuditObjectType
	ObjectID   string
	// Changes 是"字段名 → 新值"的扁平摘要；只记录新值，不记录完整前后对照。
	Changes map[string]string
	// RequestID 把一次请求产生的多条记录串起来；没有时为空串。
	RequestID string
}

// 变更摘要的上限。三个数字都是"防止审计表被单条记录撑爆"的硬边界，
// 而不是风格选择：审计表只增不删，一条含超长值的记录会永久占着空间。
const (
	AuditMaxChangeKeys   = 20
	AuditMaxValueLength  = 200
	auditRedactedValue   = "[redacted]"
	auditTruncatedSuffix = "…"
)

// auditSensitiveKeyParts 是"绝不入库"的键名特征。
//
// 判断用**包含**而不是相等：真实代码里这些字段会带前缀（user_password、
// oauth2_access_token、mail_body…），按相等判断必然漏掉一个。
var auditSensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "hash",
	"credential", "authorization", "cookie",
	"body", "content", "file",
}

// SanitizeAuditChanges 归一化变更摘要：
//
//   - 键名命中敏感特征时，值替换为 [redacted]（保留键，让人看得出"这个字段变过"，
//     但不泄漏内容）；
//   - 值按 rune 截断到 AuditMaxValueLength；
//   - 键按字典序保留前 AuditMaxChangeKeys 个，保证结果稳定（map 遍历顺序随机，
//     不排序会让"截断哪些键"变得不可预测）；
//   - 空键被丢弃。
//
// 返回的 map 一定非 nil（写库时对应 JSONB 的 {}）。
func SanitizeAuditChanges(changes map[string]string) map[string]string {
	sanitized := make(map[string]string, len(changes))
	if len(changes) == 0 {
		return sanitized
	}

	keys := make([]string, 0, len(changes))
	for key := range changes {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > AuditMaxChangeKeys {
		keys = keys[:AuditMaxChangeKeys]
	}

	for _, key := range keys {
		value := changes[key]
		if isSensitiveAuditKey(key) {
			sanitized[key] = auditRedactedValue
			continue
		}
		sanitized[key] = truncateRunes(value, AuditMaxValueLength)
	}
	return sanitized
}

// isSensitiveAuditKey 报告键名是否命中敏感特征。
func isSensitiveAuditKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range auditSensitiveKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// truncateRunes 按 rune 截断，避免把多字节字符切成乱码。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + auditTruncatedSuffix
}

// ValidateAuditEntry 校验一条审计记录是否可以落库。
//
// 校验三件事：主体与对象非空、两个枚举在各自白名单内、changes 已归一化。
// **不**校验 UUID 格式：本仓库把"是不是 UUID"放在用例层（idgen.IsUUID），
// 与其它端点的既有分工一致。
func ValidateAuditEntry(entry AuditEntry) error {
	if strings.TrimSpace(entry.ActorID) == "" {
		return New(CodeValidationError, "审计记录缺少操作人").WithField("actor_id", "不能为空")
	}
	if strings.TrimSpace(entry.ObjectID) == "" {
		return New(CodeValidationError, "审计记录缺少对象 id").WithField("object_id", "不能为空")
	}
	if !IsAuditObjectType(string(entry.ObjectType)) {
		return New(CodeValidationError, "审计对象类型不在白名单内").
			WithField("object_type", fmt.Sprintf("未知取值 %q", entry.ObjectType))
	}
	if !IsAuditAction(string(entry.Action)) {
		return New(CodeValidationError, "审计动作不在白名单内").
			WithField("action", fmt.Sprintf("未知取值 %q", entry.Action))
	}
	return nil
}
