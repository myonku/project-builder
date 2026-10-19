// Package config 负责在应用启动阶段一次性读取、解析、校验配置，并返回不可变配置对象。
//
// 优先级（低 → 高）：代码默认值 < TOML 配置文件 < .env 文件 < 进程环境变量。
// 业务代码不得在每次请求中重新读取配置源（见 backend/docs/B03-配置与运行.md）。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// 环境阶段。APP_ENV 未设置时按 development 处理，避免生产安全校验被静默跳过。
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// 开发占位密钥：生产环境中出现即启动失败。
const devSessionSecretPlaceholder = "dev-only-change-me"

// DefaultConfigFile 是未通过 APP_CONFIG_FILE 指定时的默认配置文件名。
const DefaultConfigFile = "config.toml"

// Config 是启动后不可变的全局配置对象。
type Config struct {
	APIVersion string          `toml:"API_VERSION"`
	Server     ServerConfig    `toml:"server"`
	Database   DatabaseConfig  `toml:"database"`
	Redis      RedisConfig     `toml:"redis"`
	Mail       MailConfig      `toml:"mail"`
	Storage    StorageConfig   `toml:"storage"`
	Security   SecurityConfig  `toml:"security"`
	RateLimit  RateLimitConfig `toml:"ratelimit"`
	Scheduler  SchedulerConfig `toml:"scheduler"`

	// Env 与 ConfigFile 记录配置来源，便于启动日志定位问题（不含敏感值）。
	Env        string `toml:"-"`
	ConfigFile string `toml:"-"`
}

// ServerConfig 是 HTTP 运行参数。
type ServerConfig struct {
	Host                   string   `toml:"host"`
	Port                   int      `toml:"port"`
	ReadTimeoutSeconds     int      `toml:"read_timeout_seconds"`
	WriteTimeoutSeconds    int      `toml:"write_timeout_seconds"`
	ShutdownTimeoutSeconds int      `toml:"shutdown_timeout_seconds"`
	CORSAllowedOrigins     []string `toml:"cors_allowed_origins"`
}

// DatabaseConfig 描述 PostgreSQL 16 连接参数。
type DatabaseConfig struct {
	Host                   string `toml:"host"`
	Port                   int    `toml:"port"`
	Name                   string `toml:"name"`
	User                   string `toml:"user"`
	Password               string `toml:"password"`
	SSLMode                string `toml:"ssl_mode"`
	MaxConns               int32  `toml:"max_conns"`
	MinConns               int32  `toml:"min_conns"`
	MaxConnLifetimeSeconds int    `toml:"max_conn_lifetime_seconds"`
	ConnectTimeoutSeconds  int    `toml:"connect_timeout_seconds"`
	// URL 为可选完整覆盖项（DATABASE_URL）。设置后忽略上方分项参数。
	URL string `toml:"url"`
}

// RedisConfig 描述短期状态存储。Redis 不保存核心业务唯一数据。
type RedisConfig struct {
	Enabled            bool   `toml:"enabled"`
	Host               string `toml:"host"`
	Port               int    `toml:"port"`
	DB                 int    `toml:"db"`
	Password           string `toml:"password"`
	DialTimeoutSeconds int    `toml:"dial_timeout_seconds"`
}

// MailConfig 描述 SMTP 适配器参数。默认面向 Outlook 官方 SMTP。
type MailConfig struct {
	Enabled     bool   `toml:"enabled"`
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	FromAddress string `toml:"from_address"`
	FromName    string `toml:"from_name"`
	// UseTLS 为真时在明文连接之上协商 STARTTLS（端口 587 的常规做法）。
	UseTLS bool `toml:"use_tls"`
	// ImplicitTLS 为真时直接以 TLS 连接（端口 465 的做法）。
	//
	// 与 UseTLS 分开：465 上"先明文再 STARTTLS"会直接失败，587 上直接 TLS 也连不上。
	// 合成一个布尔量必然产生"配置看起来对、但永远连不上"的状态。
	ImplicitTLS bool `toml:"implicit_tls"`
	// AuthMethod 是 SMTP 认证机制：login（默认）、plain 或 xoauth2。
	//
	// 实测 smtp-mail.outlook.com **不接受 AUTH PLAIN**（回应
	// `504 5.7.4 Unrecognized authentication type`），只接受 LOGIN 与 XOAUTH2。
	// 官方页面标注的 "OAuth2/Modern Auth" 对应 xoauth2，需要外部获取的访问令牌。
	AuthMethod string `toml:"auth_method"`
	// OAuth2AccessToken 仅在 auth_method = xoauth2 时使用。
	//
	// 只接受访问令牌：换取它所需的客户端注册信息（client_id / tenant）
	// 由部署方持有，本服务不实现一轮 OAuth2 授权码流程。
	OAuth2AccessToken string `toml:"oauth2_access_token"`
	// TaskURLTemplate 是邮件正文里任务链接的模板，用 {task_id} 占位。
	TaskURLTemplate string `toml:"task_url_template"`
}

// StorageConfig 描述附件存储。MVP 仅本地文件系统。
type StorageConfig struct {
	Backend         string `toml:"backend"`
	LocalRoot       string `toml:"local_root"`
	MaxFileSize     int64  `toml:"max_file_size"`
	MaxFilesPerUser int    `toml:"max_files_per_user"`
	// AllowedExtensions 是允许上传的扩展名白名单（含点号，如 ".pdf"）。
	//
	// 空数组或只含 "*" 表示**不限制**（需求文档 §9："扩展名白名单适当宽松"）。
	// 该配置全局生效，按扩展名匹配而不是 MIME——扩展名由用户提供的文件名决定，
	// 但**只用于白名单判断**，绝不参与存储路径的构造（见 stored_key 规则）。
	AllowedExtensions []string `toml:"allowed_extensions"`
}

// ExtensionsUnrestricted 报告扩展名白名单是否等同于"不限制"。
func (c StorageConfig) ExtensionsUnrestricted() bool {
	if len(c.AllowedExtensions) == 0 {
		return true
	}
	for _, item := range c.AllowedExtensions {
		if strings.TrimSpace(item) == "*" {
			return true
		}
	}
	return false
}

// SecurityConfig 描述会话、密码策略与引导管理员。
type SecurityConfig struct {
	SessionSecret     string `toml:"session_secret"`
	SessionTTLSeconds int    `toml:"session_ttl_seconds"`
	PasswordMinLength int    `toml:"password_min_length"`
	SecureCookies     bool   `toml:"secure_cookies"`
	SessionCookieName string `toml:"session_cookie_name"`

	// RegistrationRequiresApproval 控制自助注册是否走"提交申请 → 管理员审核"。
	//
	// 生产默认 true：POST /auth/register 只创建 registration_requests 行，不创建 users。
	// 测试/冒烟环境可显式设为 false，保留原有的"注册即 active"行为，避免把
	// 所有既有账号准备步骤都改成"注册 + 管理员审核"。它只是测试便利开关，
	// 不是业务上的第二种注册语义；生产配置不得关闭。
	RegistrationRequiresApproval bool `toml:"registration_requires_approval"`

	// 引导管理员（bootstrap administrator）。
	//
	// 背景：本系统的使用场景中普通用户不需要管理员角色，因此没有分发管理员的需求；
	// 只需要在系统初始化时录入**一个**系统级后台账号即可。而全部管理员端点都要求
	// 调用者已是管理员，所以必须有一个不依赖 HTTP 的初始化入口——就是这组配置。
	//
	// 语义（见 auth.Service.EnsureAdministrator）：
	//   - BootstrapAdminUsername 为空 ⇒ 本功能关闭，不做任何事；
	//   - 账号不存在 ⇒ 按给定的邮箱/密码/显示名创建，并直接置为 admin；
	//   - 账号已存在 ⇒ **只确保其 system_role 为 admin，绝不覆盖密码**，
	//     否则每次重启都会把管理员改过的密码打回配置里的初始值。
	//
	// 因此 BootstrapAdminPassword 是**一次性**的：账号建好之后即可从环境中移除。
	BootstrapAdminUsername    string `toml:"bootstrap_admin_username"`
	BootstrapAdminEmail       string `toml:"bootstrap_admin_email"`
	BootstrapAdminPassword    string `toml:"bootstrap_admin_password"`
	BootstrapAdminDisplayName string `toml:"bootstrap_admin_display_name"`
}

// BootstrapAdminEnabled 报告是否配置了引导管理员。
func (c SecurityConfig) BootstrapAdminEnabled() bool {
	return strings.TrimSpace(c.BootstrapAdminUsername) != ""
}

// RateLimitConfig 描述全局 HTTP 限流（单实例、按来源地址分桶的令牌桶）。
//
// 定位是"封顶"而不是精确配额（人工决策 2026-10-12）：
//   - 只做**全局中间件**，不做用户/会话级限流——按会话限流在登录之前就失效，
//     而那正是最需要挡的场景（登录口令尝试）；
//   - 默认值刻意宽松：内部工具的正常使用（含 182 项端到端冒烟）不应被限流，
//     它挡的是脚本化的高频刷取；
//   - 单实例内存态：本系统是单进程部署（与 scheduler 同理），不做分布式限流。
type RateLimitConfig struct {
	Enabled bool `toml:"enabled"`
	// RequestsPerMinute 是稳态速率（令牌桶的补充速率）。
	RequestsPerMinute int `toml:"requests_per_minute"`
	// Burst 是桶容量，也是"瞬时允许多少个请求"的上限。
	Burst int `toml:"burst"`
}

// Rate 返回每秒补充的令牌数。
func (c RateLimitConfig) Rate() float64 {
	return float64(c.RequestsPerMinute) / 60
}

// SchedulerConfig 描述单实例应用内调度参数。
type SchedulerConfig struct {
	Enabled           bool `toml:"enabled"`
	IntervalSeconds   int  `toml:"interval_seconds"`
	ReminderBatchSize int  `toml:"reminder_batch_size"`
	MaxRetries        int  `toml:"max_retries"`
}

// SessionTTL 返回会话有效期。
func (c SecurityConfig) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLSeconds) * time.Second
}

// ReadTimeout 返回 HTTP 读超时。
func (c ServerConfig) ReadTimeout() time.Duration {
	return time.Duration(c.ReadTimeoutSeconds) * time.Second
}

// WriteTimeout 返回 HTTP 写超时。
func (c ServerConfig) WriteTimeout() time.Duration {
	return time.Duration(c.WriteTimeoutSeconds) * time.Second
}

// ShutdownTimeout 返回优雅关闭预算。
func (c ServerConfig) ShutdownTimeout() time.Duration {
	return time.Duration(c.ShutdownTimeoutSeconds) * time.Second
}

// MaxConnLifetime 返回连接最长存活时间。
func (c DatabaseConfig) MaxConnLifetime() time.Duration {
	return time.Duration(c.MaxConnLifetimeSeconds) * time.Second
}

// ConnectTimeout 返回连接建立超时。
func (c DatabaseConfig) ConnectTimeout() time.Duration {
	return time.Duration(c.ConnectTimeoutSeconds) * time.Second
}

// RedisDialTimeout 返回 Redis 连接超时。
func (c RedisConfig) RedisDialTimeout() time.Duration {
	return time.Duration(c.DialTimeoutSeconds) * time.Second
}

// IsProduction 报告当前是否按生产阶段运行。
func IsProduction(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), EnvProduction)
}

// DSN 构造 pgx 可用的 PostgreSQL 连接串。
//
// 所有分项参数都经过 url 转义，因此密码中出现 @ : / 等保留字符不会破坏连接串。
// 若显式设置了完整 URL，则原样使用，由调用方保证其正确性。
func (c DatabaseConfig) DSN() string {
	if strings.TrimSpace(c.URL) != "" {
		return strings.TrimSpace(c.URL)
	}
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Name,
	}
	q := url.Values{}
	q.Set("sslmode", sslMode)
	q.Set("connect_timeout", strconv.Itoa(c.ConnectTimeoutSeconds))
	u.RawQuery = q.Encode()
	return u.String()
}

// RedactedDSN 返回脱敏连接串，仅用于日志与错误信息，绝不包含密码。
func (c DatabaseConfig) RedactedDSN() string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	if strings.TrimSpace(c.URL) != "" {
		return "postgres://<redacted>@" + "<from-url>" + "?sslmode=" + sslMode
	}
	return fmt.Sprintf("postgres://%s:<redacted>@%s:%d/%s?sslmode=%s",
		redactIfSet(c.User), c.Host, c.Port, c.Name, sslMode)
}

// Address 返回 Redis 地址。
func (c RedisConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// defaultConfig 返回全部代码默认值。任何键缺失都退回这里。
func defaultConfig() Config {
	return Config{
		APIVersion: "1",
		Server: ServerConfig{
			Host:                   "127.0.0.1",
			Port:                   8000,
			ReadTimeoutSeconds:     15,
			WriteTimeoutSeconds:    30,
			ShutdownTimeoutSeconds: 10,
			CORSAllowedOrigins:     []string{"http://127.0.0.1:5173", "http://localhost:5173"},
		},
		Database: DatabaseConfig{
			Host:                   "localhost",
			Port:                   5432,
			Name:                   "project_builder",
			User:                   "project_builder",
			Password:               "",
			SSLMode:                "disable",
			MaxConns:               10,
			MinConns:               1,
			MaxConnLifetimeSeconds: 1800,
			ConnectTimeoutSeconds:  10,
		},
		Redis: RedisConfig{
			Enabled:            true,
			Host:               "localhost",
			Port:               6379,
			DB:                 0,
			DialTimeoutSeconds: 5,
		},
		Mail: MailConfig{
			// 默认**关闭**：打开邮件就必须提供账号与发件地址，
			// 否则"启动成功但每封都发不出去"是最难排查的一种状态。
			// 因此启用邮件是一次显式动作（见 backend/docs/B03 §3.5.1）。
			Enabled: false,
			// 目标服务商为阿里邮箱：SMTP 走 smtp.qiye.aliyun.com:465 + 直接 TLS。
			// 阿里邮箱未开放 587/80（非加密只有 25），因此这里是 implicit TLS 而不是 STARTTLS；
			// 凭据必须通过环境变量注入（见 backend/docs/B03 §3.5.1）。
			Host:        "smtp.qiye.aliyun.com",
			Port:        465,
			FromName:    "项目计划管理系统",
			UseTLS:      false,
			ImplicitTLS: true,
			AuthMethod:  "login",
			// 邮件里的"任务链接"需要一个外部可达的地址，因此必须可配置。
			TaskURLTemplate: "http://localhost:8080/tasks/{task_id}",
		},
		Storage: StorageConfig{
			Backend:         "local",
			LocalRoot:       "./data/uploads",
			MaxFileSize:     10485760,
			MaxFilesPerUser: 1000,
		},
		Security: SecurityConfig{
			SessionSecret:                devSessionSecretPlaceholder,
			SessionTTLSeconds:            28800,
			PasswordMinLength:            8,
			SecureCookies:                false,
			SessionCookieName:            "pb_session",
			RegistrationRequiresApproval: true,
		},
		RateLimit: RateLimitConfig{
			// 默认开启：限流是"没配就是关着"最容易在交付时被漏掉的一类保护。
			// 速率与突发都放得很宽（见 RateLimitConfig 的注释），
			// 目的是挡住脚本化高频刷取，而不是给正常使用制造 429。
			Enabled:           true,
			RequestsPerMinute: 3000,
			Burst:             300,
		},
		Scheduler: SchedulerConfig{
			Enabled:           true,
			IntervalSeconds:   60,
			ReminderBatchSize: 100,
			MaxRetries:        1,
		},
	}
}

// envBinding 描述一个环境变量到配置字段的映射。
type envBinding struct {
	key   string
	apply func(c *Config, raw string) error
}

func stringSetter(dst func(c *Config) *string) func(*Config, string) error {
	return func(c *Config, raw string) error {
		*dst(c) = raw
		return nil
	}
}

func intSetter(dst func(c *Config) *int) func(*Config, string) error {
	return func(c *Config, raw string) error {
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("期望整数，实际为 %q", raw)
		}
		*dst(c) = v
		return nil
	}
}

func boolSetter(dst func(c *Config) *bool) func(*Config, string) error {
	return func(c *Config, raw string) error {
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("期望布尔值（true/false），实际为 %q", raw)
		}
		*dst(c) = v
		return nil
	}
}

// int32Setter 用于 int32 数值字段。
//
// 注意：早期版本这里曾用 intSetter 配合临时变量转换，导致环境变量被静默丢弃
// （*dst(c) = v 写进了一个临时 int 的地址）。该缺陷已由 config_test.go 的
// "max_conns 为零" 用例锁住。
func int32Setter(dst func(c *Config) *int32) func(*Config, string) error {
	return func(c *Config, raw string) error {
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
		if err != nil {
			return fmt.Errorf("期望 32 位整数，实际为 %q", raw)
		}
		*dst(c) = int32(v)
		return nil
	}
}

// int64Setter 用于 int64 数值字段。
// stringSliceSetter 绑定逗号分隔的字符串列表（空值表示空列表）。
//
// 配置里用数组、环境变量里用逗号分隔，两者的语义都是"列表"；
// 空字符串是**有意覆盖**（清空列表 = 不限制），与 stringSetter 的既有约定一致。
func stringSliceSetter(dst func(c *Config) *[]string) func(*Config, string) error {
	return func(c *Config, raw string) error {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			*dst(c) = nil
			return nil
		}
		parts := strings.Split(trimmed, ",")
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			if item := strings.TrimSpace(part); item != "" {
				values = append(values, item)
			}
		}
		*dst(c) = values
		return nil
	}
}

func int64Setter(dst func(c *Config) *int64) func(*Config, string) error {
	return func(c *Config, raw string) error {
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return fmt.Errorf("期望 64 位整数，实际为 %q", raw)
		}
		*dst(c) = v
		return nil
	}
}

// envBindings 是环境变量绑定表。新增配置项必须在此登记，否则无法通过环境变量覆盖。
func envBindings() []envBinding {
	return []envBinding{
		{"API_VERSION", stringSetter(func(c *Config) *string { return &c.APIVersion })},

		{"SERVER_HOST", stringSetter(func(c *Config) *string { return &c.Server.Host })},
		{"SERVER_PORT", intSetter(func(c *Config) *int { return &c.Server.Port })},
		{"SERVER_READ_TIMEOUT_SECONDS", intSetter(func(c *Config) *int { return &c.Server.ReadTimeoutSeconds })},
		{"SERVER_WRITE_TIMEOUT_SECONDS", intSetter(func(c *Config) *int { return &c.Server.WriteTimeoutSeconds })},
		{"SERVER_SHUTDOWN_TIMEOUT_SECONDS", intSetter(func(c *Config) *int { return &c.Server.ShutdownTimeoutSeconds })},
		{"SERVER_CORS_ALLOWED_ORIGINS", func(c *Config, raw string) error {
			c.Server.CORSAllowedOrigins = splitList(raw)
			return nil
		}},

		{"DATABASE_URL", stringSetter(func(c *Config) *string { return &c.Database.URL })},
		{"DATABASE_HOST", stringSetter(func(c *Config) *string { return &c.Database.Host })},
		{"DATABASE_PORT", intSetter(func(c *Config) *int { return &c.Database.Port })},
		{"DATABASE_NAME", stringSetter(func(c *Config) *string { return &c.Database.Name })},
		{"DATABASE_USER", stringSetter(func(c *Config) *string { return &c.Database.User })},
		{"DATABASE_PASSWORD", stringSetter(func(c *Config) *string { return &c.Database.Password })},
		{"DATABASE_SSL_MODE", stringSetter(func(c *Config) *string { return &c.Database.SSLMode })},
		{"DATABASE_MAX_CONNS", int32Setter(func(c *Config) *int32 { return &c.Database.MaxConns })},
		{"DATABASE_MIN_CONNS", int32Setter(func(c *Config) *int32 { return &c.Database.MinConns })},
		{"DATABASE_CONNECT_TIMEOUT_SECONDS", intSetter(func(c *Config) *int { return &c.Database.ConnectTimeoutSeconds })},

		{"REDIS_ENABLED", boolSetter(func(c *Config) *bool { return &c.Redis.Enabled })},
		{"REDIS_HOST", stringSetter(func(c *Config) *string { return &c.Redis.Host })},
		{"REDIS_PORT", intSetter(func(c *Config) *int { return &c.Redis.Port })},
		{"REDIS_DB", intSetter(func(c *Config) *int { return &c.Redis.DB })},
		{"REDIS_PASSWORD", stringSetter(func(c *Config) *string { return &c.Redis.Password })},

		{"MAIL_ENABLED", boolSetter(func(c *Config) *bool { return &c.Mail.Enabled })},
		{"MAIL_HOST", stringSetter(func(c *Config) *string { return &c.Mail.Host })},
		{"MAIL_PORT", intSetter(func(c *Config) *int { return &c.Mail.Port })},
		{"MAIL_USERNAME", stringSetter(func(c *Config) *string { return &c.Mail.Username })},
		{"MAIL_PASSWORD", stringSetter(func(c *Config) *string { return &c.Mail.Password })},
		{"MAIL_FROM_ADDRESS", stringSetter(func(c *Config) *string { return &c.Mail.FromAddress })},
		{"MAIL_FROM_NAME", stringSetter(func(c *Config) *string { return &c.Mail.FromName })},
		{"MAIL_USE_TLS", boolSetter(func(c *Config) *bool { return &c.Mail.UseTLS })},

		{"STORAGE_BACKEND", stringSetter(func(c *Config) *string { return &c.Storage.Backend })},
		{"STORAGE_LOCAL_ROOT", stringSetter(func(c *Config) *string { return &c.Storage.LocalRoot })},
		{"MAIL_AUTH_METHOD", stringSetter(func(c *Config) *string { return &c.Mail.AuthMethod })},
		{"MAIL_OAUTH2_ACCESS_TOKEN", stringSetter(func(c *Config) *string { return &c.Mail.OAuth2AccessToken })},
		{"MAIL_IMPLICIT_TLS", boolSetter(func(c *Config) *bool { return &c.Mail.ImplicitTLS })},
		{"STORAGE_MAX_FILE_SIZE", int64Setter(func(c *Config) *int64 { return &c.Storage.MaxFileSize })},
		{"STORAGE_MAX_FILES_PER_USER", intSetter(func(c *Config) *int { return &c.Storage.MaxFilesPerUser })},
		{"STORAGE_ALLOWED_EXTENSIONS", stringSliceSetter(func(c *Config) *[]string { return &c.Storage.AllowedExtensions })},

		{"SECURITY_SESSION_SECRET", stringSetter(func(c *Config) *string { return &c.Security.SessionSecret })},
		{"SECURITY_SESSION_TTL_SECONDS", intSetter(func(c *Config) *int { return &c.Security.SessionTTLSeconds })},
		{"SECURITY_PASSWORD_MIN_LENGTH", intSetter(func(c *Config) *int { return &c.Security.PasswordMinLength })},
		{"SECURITY_SECURE_COOKIES", boolSetter(func(c *Config) *bool { return &c.Security.SecureCookies })},
		{"SECURITY_SESSION_COOKIE_NAME", stringSetter(func(c *Config) *string { return &c.Security.SessionCookieName })},
		{"SECURITY_REGISTRATION_REQUIRES_APPROVAL", boolSetter(func(c *Config) *bool { return &c.Security.RegistrationRequiresApproval })},
		{"SECURITY_BOOTSTRAP_ADMIN_USERNAME", stringSetter(func(c *Config) *string { return &c.Security.BootstrapAdminUsername })},
		{"SECURITY_BOOTSTRAP_ADMIN_EMAIL", stringSetter(func(c *Config) *string { return &c.Security.BootstrapAdminEmail })},
		{"SECURITY_BOOTSTRAP_ADMIN_PASSWORD", stringSetter(func(c *Config) *string { return &c.Security.BootstrapAdminPassword })},
		{"SECURITY_BOOTSTRAP_ADMIN_DISPLAY_NAME", stringSetter(func(c *Config) *string { return &c.Security.BootstrapAdminDisplayName })},

		{"SCHEDULER_ENABLED", boolSetter(func(c *Config) *bool { return &c.Scheduler.Enabled })},
		{"SCHEDULER_INTERVAL_SECONDS", intSetter(func(c *Config) *int { return &c.Scheduler.IntervalSeconds })},
		{"SCHEDULER_REMINDER_BATCH_SIZE", intSetter(func(c *Config) *int { return &c.Scheduler.ReminderBatchSize })},
		{"SCHEDULER_MAX_RETRIES", intSetter(func(c *Config) *int { return &c.Scheduler.MaxRetries })},

		{"RATELIMIT_ENABLED", boolSetter(func(c *Config) *bool { return &c.RateLimit.Enabled })},
		{"RATELIMIT_REQUESTS_PER_MINUTE", intSetter(func(c *Config) *int { return &c.RateLimit.RequestsPerMinute })},
		{"RATELIMIT_BURST", intSetter(func(c *Config) *int { return &c.RateLimit.Burst })},
	}
}

// Load 按既定优先级读取配置并完成校验。
//
//	configPath 为空时使用 DefaultConfigFile；文件不存在不算错误（纯环境变量部署）。
//	envFilePath 为空时不读取 .env。
func Load(configPath, envFilePath string) (Config, error) {
	cfg := defaultConfig()

	if configPath == "" {
		configPath = DefaultConfigFile
	}
	// 记录 TOML 中显式出现的键，用于区分"文件设成零值"与"文件没写"。
	seen, err := decodeTOML(configPath, &cfg)
	if err != nil {
		return Config{}, err
	}
	cfg.ConfigFile = configPath
	if _, ok := seen["API_VERSION"]; !ok && cfg.APIVersion == "" {
		cfg.APIVersion = "1"
	}

	// .env 优先级高于 TOML，但不覆盖已存在的进程环境变量。
	fileEnv, err := readEnvFile(envFilePath)
	if err != nil {
		return Config{}, err
	}
	if err := applyEnv(&cfg, fileEnv, false); err != nil {
		return Config{}, err
	}

	// 进程环境变量优先级最高。
	if err := applyEnv(&cfg, processEnv(), true); err != nil {
		return Config{}, err
	}

	cfg.Env = resolveEnv()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// decodeTOML 解析 TOML 文件，返回文件中显式出现的键集合。
// 文件不存在时返回空集合，不报错。
func decodeTOML(path string, cfg *Config) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	if _, err := toml.Decode(string(raw), cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	var generic map[string]any
	if _, err := toml.Decode(string(raw), &generic); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	return generic, nil
}

// readEnvFile 读取 .env 文件。文件不存在时返回空集合。
func readEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	if path == "" {
		return values, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, fmt.Errorf("读取环境文件 %s 失败: %w", path, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("环境文件 %s 第 %d 行缺少 '=': %q", path, i+1, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// 允许可选的外层引号。
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		values[key] = value
	}
	return values, nil
}

// processEnv 快照当前进程环境变量。
func processEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		key, value, found := strings.Cut(kv, "=")
		if found {
			out[key] = value
		}
	}
	return out
}

// applyEnv 把环境变量映射到配置。process 为 true 时表示进程环境变量（最高优先级）。
func applyEnv(cfg *Config, values map[string]string, process bool) error {
	for _, b := range envBindings() {
		raw, ok := values[b.key]
		if !ok {
			continue
		}
		if process && raw == "" {
			// 进程环境变量为空串视为"未设置"，避免 CI 中的空变量把 TOML 值清掉。
			// 注意：.env 中的空串是有意覆盖（例如清空 MAIL_PASSWORD）。
			continue
		}
		if err := b.apply(cfg, raw); err != nil {
			return fmt.Errorf("环境变量 %s 非法: %w", b.key, err)
		}
	}
	return nil
}

// resolveEnv 解析运行阶段，默认 development。
func resolveEnv() string {
	raw := strings.TrimSpace(os.Getenv("APP_ENV"))
	switch strings.ToLower(raw) {
	case EnvProduction, EnvTest, EnvDevelopment:
		return strings.ToLower(raw)
	case "":
		return EnvDevelopment
	default:
		return strings.ToLower(raw)
	}
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// validate 完成范围与依赖校验，失败时快速失败并给出脱敏后的字段名与原因。
func (c Config) validate() error {
	var problems []string

	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.APIVersion) == "" {
		add("API_VERSION 不能为空")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		add("server.port 必须在 1..65535 之间，实际为 %d", c.Server.Port)
	}
	if c.Server.ReadTimeoutSeconds <= 0 {
		add("server.read_timeout_seconds 必须为正数，实际为 %d", c.Server.ReadTimeoutSeconds)
	}
	if c.Server.WriteTimeoutSeconds <= 0 {
		add("server.write_timeout_seconds 必须为正数，实际为 %d", c.Server.WriteTimeoutSeconds)
	}
	if c.Server.ShutdownTimeoutSeconds <= 0 {
		add("server.shutdown_timeout_seconds 必须为正数，实际为 %d", c.Server.ShutdownTimeoutSeconds)
	}

	if c.Database.Port < 1 || c.Database.Port > 65535 {
		add("database.port 必须在 1..65535 之间，实际为 %d", c.Database.Port)
	}
	if strings.TrimSpace(c.Database.Name) == "" {
		add("database.name 不能为空")
	}
	if strings.TrimSpace(c.Database.User) == "" {
		add("database.user 不能为空")
	}
	if c.Database.MaxConns <= 0 {
		add("database.max_conns 必须为正数，实际为 %d", c.Database.MaxConns)
	}
	if c.Database.MinConns < 0 || c.Database.MinConns > c.Database.MaxConns {
		add("database.min_conns 必须在 0..max_conns 之间，实际为 %d（max_conns=%d）",
			c.Database.MinConns, c.Database.MaxConns)
	}

	if c.Redis.Enabled {
		if c.Redis.Port < 1 || c.Redis.Port > 65535 {
			add("redis.port 必须在 1..65535 之间，实际为 %d", c.Redis.Port)
		}
		if c.Redis.DB < 0 {
			add("redis.db 不能为负数，实际为 %d", c.Redis.DB)
		}
	}

	if c.Mail.Enabled {
		if strings.TrimSpace(c.Mail.Host) == "" {
			add("mail.host 不能为空（mail.enabled = true）")
		}
		if c.Mail.Port < 1 || c.Mail.Port > 65535 {
			add("mail.port 必须在 1..65535 之间，实际为 %d", c.Mail.Port)
		}
	}

	if c.Mail.Enabled {
		if strings.TrimSpace(c.Mail.Host) == "" {
			add("mail.host 在 mail.enabled = true 时不能为空")
		}
		if c.Mail.Port <= 0 || c.Mail.Port > 65535 {
			add("mail.port 非法，实际为 %d", c.Mail.Port)
		}
		if strings.TrimSpace(c.Mail.FromAddress) == "" {
			add("mail.from_address 在 mail.enabled = true 时不能为空")
		}
		// 认证机制写成已知值之外的内容时，发送阶段才会失败——
		// 那时错误信息只会是"认证失败"，排查不到根因，因此启动即拒绝。
		switch c.Mail.AuthMethod {
		case "", "login", "plain", "xoauth2":
		default:
			add("mail.auth_method 只能是 login、plain 或 xoauth2，实际为 %q", c.Mail.AuthMethod)
		}
		if c.Mail.AuthMethod == "xoauth2" && strings.TrimSpace(c.Mail.OAuth2AccessToken) == "" {
			add("mail.auth_method = xoauth2 时必须提供 mail.oauth2_access_token")
		}
	}
	if c.Storage.MaxFileSize <= 0 {
		add("storage.max_file_size 必须为正数，实际为 %d", c.Storage.MaxFileSize)
	}
	if c.Storage.MaxFilesPerUser <= 0 {
		add("storage.max_files_per_user 必须为正数，实际为 %d", c.Storage.MaxFilesPerUser)
	}
	for _, item := range c.Storage.AllowedExtensions {
		trimmed := strings.TrimSpace(item)
		if trimmed == "*" {
			continue
		}
		if !strings.HasPrefix(trimmed, ".") || len(trimmed) < 2 {
			add("storage.allowed_extensions 的每一项都必须以点号开头（如 .pdf），实际为 %q", item)
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.Storage.Backend)) {
	case "local":
	case "":
		add("storage.backend 不能为空")
	default:
		add("storage.backend 当前只支持 local，实际为 %q", c.Storage.Backend)
	}

	if strings.TrimSpace(c.Security.SessionSecret) == "" {
		add("security.session_secret 不能为空")
	}
	if c.Security.SessionTTLSeconds <= 0 {
		add("security.session_ttl_seconds 必须为正数，实际为 %d", c.Security.SessionTTLSeconds)
	}
	if c.Security.PasswordMinLength < 8 {
		add("security.password_min_length 不得小于 8，实际为 %d", c.Security.PasswordMinLength)
	}
	if strings.TrimSpace(c.Security.SessionCookieName) == "" {
		add("security.session_cookie_name 不能为空")
	}

	// 引导管理员：要么整组都不配置（功能关闭），要么必须完整。
	//
	// 为什么不允许"只配用户名"：那会让一次本该创建管理员的初始化静默地什么都不做，
	// 而操作者以为自己已经录入完毕。宁可启动失败也不要留一个"看起来配了"的空配置。
	bootstrapUsername := strings.TrimSpace(c.Security.BootstrapAdminUsername)
	bootstrapEmail := strings.TrimSpace(c.Security.BootstrapAdminEmail)
	switch {
	case bootstrapUsername == "":
		if bootstrapEmail != "" || c.Security.BootstrapAdminPassword != "" {
			add("security.bootstrap_admin_username 为空时不得单独配置邮箱或密码（引导管理员功能要么整组配置，要么全部留空）")
		}
	case bootstrapEmail == "":
		add("配置了 security.bootstrap_admin_username 时必须同时提供 security.bootstrap_admin_email")
	case c.Security.BootstrapAdminPassword == "":
		add("配置了 security.bootstrap_admin_username 时必须同时提供 security.bootstrap_admin_password")
	}
	// 账号名/邮箱/密码的具体格式由 domain.NormalizeRegistration 在初始化时校验，
	// 那里是这三项规则的唯一定义处（避免配置层再写一套）。

	if c.Scheduler.IntervalSeconds <= 0 {
		add("scheduler.interval_seconds 必须为正数，实际为 %d", c.Scheduler.IntervalSeconds)
	}

	// 限流：关闭时不校验取值（允许用 0 表示"不关心"），开启时两项都必须为正。
	//
	// 为什么不给 burst 设一个"最小合理值"：默认值已经足够宽松，
	// 而把测试环境调成 burst=2 是验证 429 路径的正当做法
	// （集成 harness 用零值配置显式关闭限流，见 internal/testsupport/harness.go）。
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerMinute <= 0 {
			add("ratelimit.requests_per_minute 在启用限流时必须为正数，实际为 %d", c.RateLimit.RequestsPerMinute)
		}
		if c.RateLimit.Burst <= 0 {
			add("ratelimit.burst 在启用限流时必须为正数，实际为 %d", c.RateLimit.Burst)
		}
	}

	if c.Env == EnvProduction {
		// 生产环境的安全基线：不得沿用开发占位值或通配来源。
		if c.Security.SessionSecret == devSessionSecretPlaceholder {
			add("生产环境（APP_ENV=production）禁止使用默认 session_secret，请通过 SECURITY_SESSION_SECRET 注入随机密钥")
		}
		if len(c.Security.SessionSecret) < 32 {
			add("生产环境 session_secret 长度不得少于 32 字符，实际为 %d", len(c.Security.SessionSecret))
		}
		for _, origin := range c.Server.CORSAllowedOrigins {
			if strings.TrimSpace(origin) == "*" {
				add("生产环境 cors_allowed_origins 不得包含 \"*\"（Cookie 凭据模式下的已知安全缺陷）")
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("配置校验失败（%d 项）：\n  - %s", len(problems), strings.Join(problems, "\n  - "))
	}
	return nil
}

// Summary 返回可安全写入日志的配置摘要。任何敏感值只报告是否已设置。
func (c Config) Summary() []any {
	return []any{
		slog.String("env", c.Env),
		slog.String("api_version", c.APIVersion),
		slog.String("config_file", c.ConfigFile),
		slog.String("server_address", fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)),
		slog.Any("cors_allowed_origins", c.Server.CORSAllowedOrigins),
		slog.String("database_dsn", c.Database.RedactedDSN()),
		slog.String("database_password", describeSecret(c.Database.Password)),
		slog.Bool("redis_enabled", c.Redis.Enabled),
		slog.String("redis_address", c.Redis.Address()),
		slog.String("redis_password", describeSecret(c.Redis.Password)),
		slog.Bool("mail_enabled", c.Mail.Enabled),
		slog.String("mail_host", fmt.Sprintf("%s:%d", c.Mail.Host, c.Mail.Port)),
		slog.String("mail_password", describeSecret(c.Mail.Password)),
		slog.String("storage_backend", c.Storage.Backend),
		slog.String("storage_local_root", c.Storage.LocalRoot),
		slog.String("session_secret", describeSecret(c.Security.SessionSecret)),
		slog.Bool("secure_cookies", c.Security.SecureCookies),
		slog.Int("session_ttl_seconds", c.Security.SessionTTLSeconds),
		// 引导管理员：用户名不是敏感值，输出它便于运维确认初始化对象；
		// 密码只报告是否已配置。
		slog.Bool("bootstrap_admin_enabled", c.Security.BootstrapAdminEnabled()),
		slog.String("bootstrap_admin_username", c.Security.BootstrapAdminUsername),
		slog.String("bootstrap_admin_password", describeSecret(c.Security.BootstrapAdminPassword)),
		slog.Bool("scheduler_enabled", c.Scheduler.Enabled),
		slog.Bool("ratelimit_enabled", c.RateLimit.Enabled),
		slog.Int("ratelimit_requests_per_minute", c.RateLimit.RequestsPerMinute),
		slog.Int("ratelimit_burst", c.RateLimit.Burst),
	}
}

// describeSecret 只报告敏感值是否已配置，绝不输出内容。
func describeSecret(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unset"
	}
	return "set"
}

func redactIfSet(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return value
}
