// Package config 负责在应用启动阶段一次性读取、解析、校验配置，并返回不可变配置对象。
//
// 优先级（低 → 高）：代码默认值 < TOML 配置文件 < .env 文件 < 进程环境变量。
// 业务代码不得在每次请求中重新读取配置源（见 docs/03-配置与运行.md）。
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
	UseTLS      bool   `toml:"use_tls"`
}

// StorageConfig 描述附件存储。MVP 仅本地文件系统。
type StorageConfig struct {
	Backend         string `toml:"backend"`
	LocalRoot       string `toml:"local_root"`
	MaxFileSize     int64  `toml:"max_file_size"`
	MaxFilesPerUser int    `toml:"max_files_per_user"`
}

// SecurityConfig 描述会话与密码策略。
type SecurityConfig struct {
	SessionSecret     string `toml:"session_secret"`
	SessionTTLSeconds int    `toml:"session_ttl_seconds"`
	PasswordMinLength int    `toml:"password_min_length"`
	SecureCookies     bool   `toml:"secure_cookies"`
	SessionCookieName string `toml:"session_cookie_name"`
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
			Enabled:  true,
			Host:     "smtp.office365.com",
			Port:     587,
			FromName: "项目计划管理系统",
			UseTLS:   true,
		},
		Storage: StorageConfig{
			Backend:         "local",
			LocalRoot:       "./data/uploads",
			MaxFileSize:     10485760,
			MaxFilesPerUser: 1000,
		},
		Security: SecurityConfig{
			SessionSecret:     devSessionSecretPlaceholder,
			SessionTTLSeconds: 28800,
			PasswordMinLength: 8,
			SecureCookies:     false,
			SessionCookieName: "pb_session",
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
		{"STORAGE_MAX_FILE_SIZE", int64Setter(func(c *Config) *int64 { return &c.Storage.MaxFileSize })},

		{"SECURITY_SESSION_SECRET", stringSetter(func(c *Config) *string { return &c.Security.SessionSecret })},
		{"SECURITY_SESSION_TTL_SECONDS", intSetter(func(c *Config) *int { return &c.Security.SessionTTLSeconds })},
		{"SECURITY_PASSWORD_MIN_LENGTH", intSetter(func(c *Config) *int { return &c.Security.PasswordMinLength })},
		{"SECURITY_SECURE_COOKIES", boolSetter(func(c *Config) *bool { return &c.Security.SecureCookies })},
		{"SECURITY_SESSION_COOKIE_NAME", stringSetter(func(c *Config) *string { return &c.Security.SessionCookieName })},

		{"SCHEDULER_ENABLED", boolSetter(func(c *Config) *bool { return &c.Scheduler.Enabled })},
		{"SCHEDULER_INTERVAL_SECONDS", intSetter(func(c *Config) *int { return &c.Scheduler.IntervalSeconds })},
		{"SCHEDULER_REMINDER_BATCH_SIZE", intSetter(func(c *Config) *int { return &c.Scheduler.ReminderBatchSize })},
		{"SCHEDULER_MAX_RETRIES", intSetter(func(c *Config) *int { return &c.Scheduler.MaxRetries })},
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

	if c.Storage.MaxFileSize <= 0 {
		add("storage.max_file_size 必须为正数，实际为 %d", c.Storage.MaxFileSize)
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

	if c.Scheduler.IntervalSeconds <= 0 {
		add("scheduler.interval_seconds 必须为正数，实际为 %d", c.Scheduler.IntervalSeconds)
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
		slog.Bool("scheduler_enabled", c.Scheduler.Enabled),
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
