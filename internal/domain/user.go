package domain

import (
	"regexp"
	"strings"
)

// 用户状态与系统角色的稳定机器值（与数据库 CHECK 约束一致）。
const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"

	UserSystemRoleMember = "member"
	UserSystemRoleAdmin  = "admin"
)

// 字段长度上限，必须与 docs/02-数据库模型与迁移.md 3.1 的列定义一致。
const (
	UsernameMaxLength    = 64
	UsernameMinLength    = 3
	EmailMaxLength       = 320
	DisplayNameMaxLength = 128
	TimezoneMaxLength    = 64
)

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// 保持保守：只做基本语法校验，不做 RFC 5322 全解析。
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$`)
)

// User 是用户领域实体。不含任何持久化或 HTTP 类型标注。
type User struct {
	ID              string
	Username        string
	Email           string
	DisplayName     string
	PasswordHash    string
	Status          string
	SystemRole      string
	Timezone        string
	ReminderEnabled bool
}

// IsActive 报告用户是否可用。停用用户不能登录（需求文档 5.1）。
func (u User) IsActive() bool { return u.Status == UserStatusActive }

// IsAdmin 报告用户是否为系统管理员。
func (u User) IsAdmin() bool { return u.SystemRole == UserSystemRoleAdmin }

// RegistrationInput 是注册用例的输入。校验规则集中在领域层，
// 避免在 HTTP handler 里复制一份（见 docs/01-Go服务端架构设计.md 4.4 保留边界）。
type RegistrationInput struct {
	Username    string
	Email       string
	DisplayName string
	Password    string
	Timezone    string
}

// NormalizeRegistration 清洗注册输入，返回归一化后的值与字段级错误。
//
// 归一化策略：用户名与邮箱保存为小写，避免 "Alice" 与 "alice" 注册出两个账号；
// 数据库侧另有 lower(username)/lower(email) 唯一索引作为最终保证。
func NormalizeRegistration(in RegistrationInput, passwordMinLength int) (RegistrationInput, map[string]string) {
	fields := map[string]string{}

	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Timezone = strings.TrimSpace(in.Timezone)

	switch {
	case in.Username == "":
		fields["username"] = "用户名不能为空"
	case len(in.Username) < UsernameMinLength || len(in.Username) > UsernameMaxLength:
		fields["username"] = "用户名长度必须为 3 到 64 个字符"
	case !usernamePattern.MatchString(in.Username):
		fields["username"] = "用户名只能包含字母、数字、下划线和连字符"
	}

	switch {
	case in.Email == "":
		fields["email"] = "邮箱不能为空"
	case len(in.Email) > EmailMaxLength:
		fields["email"] = "邮箱长度不得超过 320 个字符"
	case !emailPattern.MatchString(in.Email):
		fields["email"] = "邮箱格式不正确"
	}

	switch {
	case in.DisplayName == "":
		fields["display_name"] = "显示名称不能为空"
	case len([]rune(in.DisplayName)) > DisplayNameMaxLength:
		fields["display_name"] = "显示名称不得超过 128 个字符"
	}

	// 密码不做 Trim：前后空格是合法密码字符。
	switch {
	case in.Password == "":
		fields["password"] = "密码不能为空"
	case passwordMinLength > 0 && len(in.Password) < passwordMinLength:
		fields["password"] = "密码长度不符合最小长度要求"
	case len([]rune(in.Password)) > 256:
		fields["password"] = "密码不得超过 256 个字符"
	}

	switch {
	case in.Timezone == "":
		// 默认按 UTC 存储；展示与提醒计算时再按用户时区转换。
		in.Timezone = "UTC"
	case len(in.Timezone) > TimezoneMaxLength:
		fields["timezone"] = "时区标识长度不得超过 64 个字符"
	default:
		if _, err := loadLocation(in.Timezone); err != nil {
			fields["timezone"] = "时区标识不是有效的 IANA 时区"
		}
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// LoginInput 是登录用例的输入。identifier 可为用户名或邮箱。
type LoginInput struct {
	Identifier string
	Password   string
}

// NormalizeLogin 清洗登录输入。
func NormalizeLogin(in LoginInput) (LoginInput, map[string]string) {
	fields := map[string]string{}
	in.Identifier = strings.ToLower(strings.TrimSpace(in.Identifier))
	if in.Identifier == "" {
		fields["identifier"] = "用户名或邮箱不能为空"
	}
	if in.Password == "" {
		fields["password"] = "密码不能为空"
	}
	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// LooksLikeEmail 判断 identifier 是否按邮箱处理，用于选择查询列。
// 该判断只影响查询路径，不影响凭据校验结果。
func LooksLikeEmail(identifier string) bool {
	return strings.Contains(identifier, "@")
}

// TimezoneOrDefault 返回用户时区，未设置时回退到给定默认值。
func TimezoneOrDefault(userTimezone, fallback string) string {
	if strings.TrimSpace(userTimezone) == "" {
		if strings.TrimSpace(fallback) == "" {
			return "UTC"
		}
		return fallback
	}
	return userTimezone
}

// UserProfileUpdate 是用户资料更新的输入。
//
// 指针字段的 nil 语义是"未提供，不修改"。这不是 Go 的惯用写法，
// 但没有它就无法区分"把 reminder_enabled 设为 false"与"不修改该字段"——
// 用零值表达"未提供"会让"关闭提醒"变成一个无法表达的操作。
type UserProfileUpdate struct {
	DisplayName     *string
	Email           *string
	Timezone        *string
	ReminderEnabled *bool
	Status          *string
	SystemRole      *string
}

// HasChanges 报告请求是否至少包含一个可修改字段。
//
// 用途：拒绝"只带 expected_updated_at"的空更新。允许它会让一次没有任何改动的请求
// 推进 updated_at，使其他客户端手里的版本无谓失效（契约 2.6 的版本协议要求
// updated_at 只在真实变更时前进，否则"版本变了"就不再等于"数据变了"）。
func (u UserProfileUpdate) HasChanges() bool {
	return u.DisplayName != nil || u.Email != nil || u.Timezone != nil ||
		u.ReminderEnabled != nil || u.Status != nil || u.SystemRole != nil
}

// NormalizeUserProfileUpdate 清洗用户资料更新输入，返回归一化后的值与字段级错误。
//
// 两条必须遵守的约定：
//
//  1. nil 表示"未提供"，归一化**不得**把已提供的字段变成 nil。
//     漏传字段会让更新被静默忽略：没有错误、没有 5xx，只有一个字段没生效
//     （见 docs/06-迁移进度与验证.md 5.5 第 5 条）。调用方另有 assertNotDropped 断言。
//  2. 只有显式提供的字符串才参与校验；未提供的字段不产生任何字段级错误。
func NormalizeUserProfileUpdate(in UserProfileUpdate) (UserProfileUpdate, map[string]string) {
	fields := map[string]string{}

	if in.DisplayName != nil {
		value := strings.TrimSpace(*in.DisplayName)
		switch {
		case value == "":
			fields["display_name"] = "显示名称不能为空"
		case len([]rune(value)) > DisplayNameMaxLength:
			fields["display_name"] = "显示名称不得超过 128 个字符"
		}
		in.DisplayName = &value
	}

	if in.Email != nil {
		value := strings.ToLower(strings.TrimSpace(*in.Email))
		switch {
		case value == "":
			fields["email"] = "邮箱不能为空"
		case len(value) > EmailMaxLength:
			fields["email"] = "邮箱长度不得超过 320 个字符"
		case !emailPattern.MatchString(value):
			fields["email"] = "邮箱格式不正确"
		}
		in.Email = &value
	}

	if in.Timezone != nil {
		value := strings.TrimSpace(*in.Timezone)
		switch {
		case value == "":
			// 注册时"省略或为空"默认 UTC；更新时清空时区没有业务含义，
			// 只会让"今天"的判定基准变得不可预期，因此显式拒绝而不是回退到 UTC。
			fields["timezone"] = "时区标识不能为空"
		case len(value) > TimezoneMaxLength:
			fields["timezone"] = "时区标识长度不得超过 64 个字符"
		default:
			if _, err := loadLocation(value); err != nil {
				fields["timezone"] = "时区标识不是有效的 IANA 时区"
			}
		}
		in.Timezone = &value
	}

	if in.Status != nil && !containsString(UserStatuses, *in.Status) {
		fields["status"] = "状态只能是 active 或 disabled"
	}

	if in.SystemRole != nil && !containsString(UserSystemRoles, *in.SystemRole) {
		fields["system_role"] = "系统角色只能是 member 或 admin"
	}

	if len(fields) > 0 {
		return in, fields
	}
	return in, nil
}

// ApplyUserProfileUpdate 返回把更新套用到 current 之后的用户实体。
//
// 用途：判定"更新后是否仍是启用中的管理员"必须使用**合并后**的取值。
// 只看请求体里的部分字段会把"仅修改邮箱"误判成角色或状态变更。
func ApplyUserProfileUpdate(current User, update UserProfileUpdate) User {
	merged := current
	if update.DisplayName != nil {
		merged.DisplayName = *update.DisplayName
	}
	if update.Email != nil {
		merged.Email = *update.Email
	}
	if update.Timezone != nil {
		merged.Timezone = *update.Timezone
	}
	if update.ReminderEnabled != nil {
		merged.ReminderEnabled = *update.ReminderEnabled
	}
	if update.Status != nil {
		merged.Status = *update.Status
	}
	if update.SystemRole != nil {
		merged.SystemRole = *update.SystemRole
	}
	return merged
}

// WouldRemoveLastAdmin 报告一次资料更新是否会让系统失去全部"启用中的管理员"。
//
// 为什么这条不变式必须由应用层守住：管理员是唯一能停用用户、修改系统角色和处理
// 全局异常的入口（需求文档 4.1）。若允许把最后一个启用中的管理员降级或停用，
// 系统会永久失去管理能力，且没有任何接口可以恢复——只能直接改数据库。
//
// 数据库无法表达"至少存在一行满足条件"这类跨行约束，因此这里用
// "排除目标用户后的活跃管理员计数"来判定，而不是先读后写的猜测：
//
//	参数 wasActiveAdmin     目标用户当前是否为 status=active 且 system_role=admin
//	参数 willBeActiveAdmin  按本次更新合并后，目标用户是否仍为启用中的管理员
//	参数 otherActiveAdmins  排除目标用户之外的启用中管理员数量
func WouldRemoveLastAdmin(wasActiveAdmin, willBeActiveAdmin bool, otherActiveAdmins int) bool {
	if !wasActiveAdmin || willBeActiveAdmin {
		return false
	}
	return otherActiveAdmins == 0
}
