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
