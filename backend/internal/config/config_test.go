package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 在临时目录写入文件并返回路径。
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
	return path
}

// clearConfigEnv 清空所有会干扰用例的环境变量。
//
// 这一步是必须的：宿主机上很可能已设置 DATABASE_* 或 SECURITY_*，
// 不清空就会出现"本机通过、CI 失败"的假象。
func clearConfigEnv(t *testing.T) {
	t.Helper()

	keys := []string{"APP_ENV", "APP_CONFIG_FILE"}
	for _, binding := range envBindings() {
		keys = append(keys, binding.key)
	}
	for _, key := range keys {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
}

func TestLoadUsesCodeDefaultsWhenNoFile(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}

	if cfg.Server.Port != 8000 {
		t.Errorf("server.port = %d，期望默认 8000", cfg.Server.Port)
	}
	if cfg.Database.Name != "project_builder" {
		t.Errorf("database.name = %q，期望默认 project_builder", cfg.Database.Name)
	}
	if cfg.Security.SessionCookieName != "pb_session" {
		t.Errorf("session_cookie_name = %q，期望默认 pb_session", cfg.Security.SessionCookieName)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("env = %q，期望默认 development", cfg.Env)
	}
	// 缺失配置文件不算错误（纯环境变量部署场景）。
	if cfg.ConfigFile == "" {
		t.Error("ConfigFile 应记录解析路径")
	}
}

func TestLoadReadsTOML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	path := writeFile(t, dir, "config.toml", `
API_VERSION = "2"

[server]
host = "0.0.0.0"
port = 9090
cors_allowed_origins = ["https://plan.example.com"]

[database]
host = "db.internal"
port = 6543
name = "pb"
user = "pb_user"
password = "toml-password"
ssl_mode = "require"
max_conns = 25

[security]
session_secret = "toml-secret-value-for-tests"
session_ttl_seconds = 7200
password_min_length = 12
`)

	cfg, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}

	if cfg.APIVersion != "2" {
		t.Errorf("api version = %q，期望 2", cfg.APIVersion)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 9090 {
		t.Errorf("server = %s:%d，期望 0.0.0.0:9090", cfg.Server.Host, cfg.Server.Port)
	}
	if cfg.Database.Name != "pb" || cfg.Database.MaxConns != 25 {
		t.Errorf("database 未按 TOML 解析: name=%q max_conns=%d", cfg.Database.Name, cfg.Database.MaxConns)
	}
	if cfg.Security.SessionTTLSeconds != 7200 {
		t.Errorf("session_ttl_seconds = %d，期望 7200", cfg.Security.SessionTTLSeconds)
	}
	// 未在 TOML 中出现的键必须保留代码默认值。
	if cfg.Redis.Port != 6379 {
		t.Errorf("redis.port = %d，期望保留默认 6379", cfg.Redis.Port)
	}
	if cfg.Storage.MaxFileSize != 10485760 {
		t.Errorf("storage.max_file_size = %d，期望保留默认值", cfg.Storage.MaxFileSize)
	}
}

// TestLoadEnvOverridesTOML 是本包最重要的用例：优先级顺序必须与文档一致。
func TestLoadEnvOverridesTOML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	path := writeFile(t, dir, "config.toml", `
[server]
host = "127.0.0.1"
port = 8000

[database]
password = "from-toml"

[security]
session_secret = "toml-secret-value-for-tests"
password_min_length = 8
`)

	envPath := writeFile(t, dir, ".env", `
SERVER_HOST=from-dotenv
DATABASE_PASSWORD=from-dotenv
`)

	// .env 覆盖 TOML。
	cfg, err := Load(path, envPath)
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	if cfg.Server.Host != "from-dotenv" {
		t.Errorf("server.host = %q，期望 .env 覆盖为 from-dotenv", cfg.Server.Host)
	}
	if cfg.Database.Password != "from-dotenv" {
		t.Errorf("database.password 未被 .env 覆盖")
	}

	// 进程环境变量覆盖 .env 与 TOML。
	os.Setenv("SERVER_HOST", "from-process")
	os.Setenv("SERVER_PORT", "9999")
	t.Cleanup(func() {
		os.Unsetenv("SERVER_HOST")
		os.Unsetenv("SERVER_PORT")
	})

	cfg, err = Load(path, envPath)
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	if cfg.Server.Host != "from-process" {
		t.Errorf("server.host = %q，期望进程环境变量优先", cfg.Server.Host)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("server.port = %d，期望 9999", cfg.Server.Port)
	}
}

// TestMailTaskURLTemplate verifies shipped defaults and deployment overrides.
func TestMailTaskURLTemplate(t *testing.T) {
	clearConfigEnv(t)
	want := "http://localhost:5173/projects/{project_id}?taskId={task_id}"
	for _, path := range []string{filepath.Join(t.TempDir(), "absent.toml"), filepath.Join("..", "..", "config.toml")} {
		cfg, err := Load(path, "")
		if err != nil || cfg.Mail.TaskURLTemplate != want {
			t.Fatalf("mail task template from %s = %q, error %v; want %q", path, cfg.Mail.TaskURLTemplate, err, want)
		}
	}
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", "[mail]\ntask_url_template = 'https://toml.test/projects/{project_id}?taskId={task_id}'\n")
	dotenvURL := "https://dotenv.test/app/projects/{project_id}?taskId={task_id}"
	envPath := writeFile(t, dir, ".env", "MAIL_TASK_URL_TEMPLATE="+dotenvURL+"\n")
	cfg, err := Load(path, envPath)
	if err != nil || cfg.Mail.TaskURLTemplate != dotenvURL {
		t.Fatalf("dotenv task template = %q, error %v", cfg.Mail.TaskURLTemplate, err)
	}
	processURL := "https://process.test/worklane/projects/{project_id}?taskId={task_id}"
	t.Setenv("MAIL_TASK_URL_TEMPLATE", processURL)
	cfg, err = Load(path, envPath)
	if err != nil || cfg.Mail.TaskURLTemplate != processURL {
		t.Fatalf("process task template = %q, error %v", cfg.Mail.TaskURLTemplate, err)
	}
}

// TestLoadIgnoresEmptyProcessEnv 覆盖 CI 中常见问题：空环境变量不应清掉配置文件的值。
func TestLoadIgnoresEmptyProcessEnv(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	path := writeFile(t, dir, "config.toml", `
[server]
host = "from-toml"
`)

	os.Setenv("SERVER_HOST", "")
	t.Cleanup(func() { os.Unsetenv("SERVER_HOST") })

	cfg, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	if cfg.Server.Host != "from-toml" {
		t.Fatalf("server.host = %q，空的进程环境变量不应覆盖 TOML", cfg.Server.Host)
	}
}

// TestLoadRejectsInvalidValues 覆盖启动快速失败。
func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		toml     string
		env      map[string]string
		wantWord string
	}{
		{
			name:     "端口越界",
			env:      map[string]string{"SERVER_PORT": "70000"},
			wantWord: "server.port",
		},
		{
			name:     "读超时为负",
			env:      map[string]string{"SERVER_READ_TIMEOUT_SECONDS": "-1"},
			wantWord: "server.read_timeout_seconds",
		},
		{
			name:     "数据库名为空",
			env:      map[string]string{"DATABASE_NAME": " "},
			wantWord: "database.name",
		},
		{
			name:     "max_conns 为零",
			env:      map[string]string{"DATABASE_MAX_CONNS": "0"},
			wantWord: "database.max_conns",
		},
		{
			name:     "min_conns 大于 max_conns",
			toml:     "[database]\nmax_conns = 5\nmin_conns = 9\n",
			wantWord: "database.min_conns",
		},
		{
			name:     "会话密钥为空",
			env:      map[string]string{"SECURITY_SESSION_SECRET": " "},
			wantWord: "security.session_secret",
		},
		{
			name:     "密码最小长度不足",
			env:      map[string]string{"SECURITY_PASSWORD_MIN_LENGTH": "4"},
			wantWord: "security.password_min_length",
		},
		{
			name:     "存储后端不支持",
			env:      map[string]string{"STORAGE_BACKEND": "s3"},
			wantWord: "storage.backend",
		},
		{
			name:     "gridfs 缺少 Mongo 连接串",
			env:      map[string]string{"STORAGE_BACKEND": "gridfs", "MONGO_URI": " "},
			wantWord: "mongo.uri",
		},
		{
			name: "gridfs 两个 bucket 相同",
			env: map[string]string{
				"STORAGE_BACKEND":       "gridfs",
				"STORAGE_GRIDFS_BUCKET": "same",
				"STORAGE_AVATAR_BUCKET": "same",
			},
			wantWord: "storage.avatar_bucket",
		},
		{
			name:     "邮件启用但主机为空",
			env:      map[string]string{"MAIL_ENABLED": "true", "MAIL_HOST": " "},
			wantWord: "mail.host",
		},
		{
			name:     "调度间隔为零",
			env:      map[string]string{"SCHEDULER_INTERVAL_SECONDS": "0"},
			wantWord: "scheduler.interval_seconds",
		},
		{
			name:     "限流速率非正",
			env:      map[string]string{"RATELIMIT_REQUESTS_PER_MINUTE": "0"},
			wantWord: "ratelimit.requests_per_minute",
		},
		{
			name:     "限流突发非正",
			toml:     "[ratelimit]\nenabled = true\nrequests_per_minute = 600\nburst = 0\n",
			wantWord: "ratelimit.burst",
		},
		{
			name:     "布尔值非法",
			env:      map[string]string{"REDIS_ENABLED": "maybe"},
			wantWord: "REDIS_ENABLED",
		},
		{
			name:     "整数非法",
			env:      map[string]string{"SERVER_PORT": "abc"},
			wantWord: "SERVER_PORT",
		},
		{
			// 只配用户名会让一次本该创建管理员的初始化静默地什么都不做，
			// 而操作者以为自己已经录入完毕。宁可启动失败。
			name:     "引导管理员只配了用户名",
			env:      map[string]string{"SECURITY_BOOTSTRAP_ADMIN_USERNAME": "admin"},
			wantWord: "security.bootstrap_admin_email",
		},
		{
			name: "引导管理员缺密码",
			env: map[string]string{
				"SECURITY_BOOTSTRAP_ADMIN_USERNAME": "admin",
				"SECURITY_BOOTSTRAP_ADMIN_EMAIL":    "admin@example.com",
			},
			wantWord: "security.bootstrap_admin_password",
		},
		{
			name:     "引导管理员未配用户名却配了密码",
			env:      map[string]string{"SECURITY_BOOTSTRAP_ADMIN_PASSWORD": "some-password"},
			wantWord: "security.bootstrap_admin_username",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			dir := t.TempDir()

			path := ""
			if tc.toml != "" {
				path = writeFile(t, dir, "config.toml", tc.toml)
			} else {
				// 保证默认的 dev 会话密钥不会掩盖被测问题。
				path = writeFile(t, dir, "config.toml", "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			_, err := Load(path, "")
			if err == nil {
				t.Fatalf("期望校验失败，实际通过")
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Fatalf("错误信息应包含 %q，实际为: %v", tc.wantWord, err)
			}
		})
	}
}

// TestLoadRejectsProductionUnsafeDefaults 覆盖生产环境安全基线。
func TestLoadRejectsProductionUnsafeDefaults(t *testing.T) {
	t.Run("沿用开发占位会话密钥", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", "")

		t.Setenv("APP_ENV", EnvProduction)
		// 使用默认的 dev-only-change-me 占位值。
		_, err := Load(path, "")
		if err == nil {
			t.Fatal("生产环境必须拒绝默认 session_secret")
		}
		if !strings.Contains(err.Error(), "session_secret") {
			t.Fatalf("错误应指出 session_secret，实际: %v", err)
		}
	})

	t.Run("会话密钥过短", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", "")

		t.Setenv("APP_ENV", EnvProduction)
		t.Setenv("SECURITY_SESSION_SECRET", "too-short")
		_, err := Load(path, "")
		if err == nil {
			t.Fatal("生产环境必须拒绝过短的 session_secret")
		}
	})

	t.Run("CORS 通配来源", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", "")

		t.Setenv("APP_ENV", EnvProduction)
		t.Setenv("SECURITY_SESSION_SECRET", strings.Repeat("s", 48))
		t.Setenv("SERVER_CORS_ALLOWED_ORIGINS", "*")
		_, err := Load(path, "")
		if err == nil {
			t.Fatal("生产环境必须拒绝 CORS 通配来源")
		}
		if !strings.Contains(err.Error(), "cors_allowed_origins") {
			t.Fatalf("错误应指出 cors_allowed_origins，实际: %v", err)
		}
	})

	t.Run("合规的生产配置可通过", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", "")

		t.Setenv("APP_ENV", EnvProduction)
		t.Setenv("SECURITY_SESSION_SECRET", strings.Repeat("s", 48))
		t.Setenv("SECURITY_SECURE_COOKIES", "true")
		t.Setenv("SERVER_CORS_ALLOWED_ORIGINS", "https://plan.example.com")

		cfg, err := Load(path, "")
		if err != nil {
			t.Fatalf("合规的生产配置不应失败: %v", err)
		}
		if !cfg.Security.SecureCookies {
			t.Error("生产环境应启用 secure_cookies")
		}
	})
}

// TestDSNEscapesReservedCharacters 覆盖连接串构造的转义正确性。
//
// 密码中出现 @ : / ? # 等保留字符时，未转义会静默连接到错误的主机或库，
// 这是非常难排查的故障，必须由测试锁住。
func TestDSNEscapesReservedCharacters(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	cfg.Database.Host = "localhost"
	cfg.Database.Port = 5432
	cfg.Database.Name = "project_builder"
	cfg.Database.User = "pb_user"
	cfg.Database.Password = "p@ss:w/ord?#1"
	cfg.Database.SSLMode = "disable"

	dsn := cfg.Database.DSN()

	for _, raw := range []string{"p@ss", ":w/ord", "?#1"} {
		if strings.Contains(dsn, raw) {
			t.Errorf("DSN 未转义密码中的保留字符 %q: %s", raw, dsn)
		}
	}
	if !strings.HasPrefix(dsn, "postgres://pb_user:") {
		t.Errorf("DSN 前缀不正确: %s", dsn)
	}
	if !strings.Contains(dsn, "localhost:5432/project_builder") {
		t.Errorf("DSN 缺少主机与库名: %s", dsn)
	}
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("DSN 缺少 sslmode: %s", dsn)
	}
}

// TestRedactedDSNNeverContainsPassword 覆盖日志脱敏。
func TestRedactedDSNNeverContainsPassword(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	cfg.Database.Password = "super-secret-password"

	redacted := cfg.Database.RedactedDSN()
	if strings.Contains(redacted, "super-secret-password") {
		t.Fatalf("脱敏连接串泄漏了密码: %s", redacted)
	}
	if !strings.Contains(redacted, "<redacted>") {
		t.Fatalf("脱敏连接串应显式标注脱敏标记: %s", redacted)
	}
}

// TestSummaryRedactsSecrets 覆盖启动日志摘要。
func TestSummaryRedactsSecrets(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	cfg.Database.Password = "db-secret-value"
	cfg.Redis.Password = "redis-secret-value"
	cfg.Mail.Password = "smtp-secret-value"
	cfg.Security.SessionSecret = "session-secret-value"
	cfg.Security.BootstrapAdminUsername = "bootstrap_admin"
	cfg.Security.BootstrapAdminPassword = "bootstrap-secret-value"

	var rendered strings.Builder
	for _, attr := range cfg.Summary() {
		fmt.Fprintf(&rendered, "%v ", attr)
	}
	summary := rendered.String()

	for _, secret := range []string{
		"db-secret-value", "redis-secret-value", "smtp-secret-value", "session-secret-value",
		"bootstrap-secret-value",
	} {
		if strings.Contains(summary, secret) {
			t.Fatalf("启动摘要泄漏了敏感值 %q: %s", secret, summary)
		}
	}
	// 但必须报告"已配置"，否则无法确认部署是否接入了凭据。
	if !strings.Contains(summary, "session_secret=set") {
		t.Errorf("摘要应报告 session_secret 已配置: %s", summary)
	}
	if !strings.Contains(summary, "bootstrap_admin_password=set") {
		t.Errorf("摘要应报告引导管理员密码已配置: %s", summary)
	}
	// 用户名不是敏感值，输出它便于运维确认初始化对象。
	if !strings.Contains(summary, "bootstrap_admin_username=bootstrap_admin") {
		t.Errorf("摘要应输出引导管理员用户名: %s", summary)
	}
}

// TestBootstrapAdminConfig 覆盖引导管理员的配置语义与边界。
func TestBootstrapAdminConfig(t *testing.T) {
	t.Run("整组配置后生效", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", `
[security]
bootstrap_admin_username = "admin"
bootstrap_admin_email = "admin@example.com"
bootstrap_admin_password = "bootstrap-password"
`)
		cfg, err := Load(path, "")
		if err != nil {
			t.Fatalf("Load 返回错误: %v", err)
		}
		if !cfg.Security.BootstrapAdminEnabled() {
			t.Fatal("配置完整时应视为已启用")
		}
		if cfg.Security.BootstrapAdminDisplayName != "" {
			t.Errorf("未提供显示名时应为空，实际为 %q", cfg.Security.BootstrapAdminDisplayName)
		}
	})

	t.Run("全部留空时功能关闭", func(t *testing.T) {
		clearConfigEnv(t)
		cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
		if err != nil {
			t.Fatalf("Load 返回错误: %v", err)
		}
		if cfg.Security.BootstrapAdminEnabled() {
			t.Fatal("未配置用户名时应视为功能关闭")
		}
	})

	t.Run("只配空白用户名也算关闭", func(t *testing.T) {
		clearConfigEnv(t)
		dir := t.TempDir()
		path := writeFile(t, dir, "config.toml", "[security]\nbootstrap_admin_username = \"   \"\n")
		cfg, err := Load(path, "")
		if err != nil {
			t.Fatalf("Load 返回错误: %v", err)
		}
		if cfg.Security.BootstrapAdminEnabled() {
			t.Fatal("空白用户名不应被视为已启用")
		}
	})
}

// TestDescribeSecret 覆盖敏感值描述的三种状态。
func TestDescribeSecret(t *testing.T) {
	tests := map[string]string{
		"":     "unset",
		"   ":  "unset",
		"abc":  "set",
		"a b ": "set",
	}
	for input, want := range tests {
		if got := describeSecret(input); got != want {
			t.Errorf("describeSecret(%q) = %q，期望 %q", input, got, want)
		}
	}
}

// TestReadEnvFileParsesQuotesAndComments 覆盖 .env 解析细节。
func TestReadEnvFileParsesQuotesAndComments(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".env", `
# 注释行
SERVER_HOST=plain-value
DATABASE_PASSWORD="quoted value"
MAIL_PASSWORD='single quoted'
EMPTY_VALUE=
WITH_EQUALS=a=b=c
`)

	values, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("readEnvFile 返回错误: %v", err)
	}

	tests := map[string]string{
		"SERVER_HOST":       "plain-value",
		"DATABASE_PASSWORD": "quoted value",
		"MAIL_PASSWORD":     "single quoted",
		"EMPTY_VALUE":       "",
		"WITH_EQUALS":       "a=b=c",
	}
	for key, want := range tests {
		if got := values[key]; got != want {
			t.Errorf("%s = %q，期望 %q", key, got, want)
		}
	}
	if _, ok := values["# 注释行"]; ok {
		t.Error("注释行不应被解析")
	}
}

// TestReadEnvFileRejectsMalformedLine 覆盖格式错误的快速失败。
func TestReadEnvFileRejectsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".env", "THIS_LINE_HAS_NO_EQUALS\n")

	if _, err := readEnvFile(path); err == nil {
		t.Fatal("缺少 '=' 的行必须报错")
	}
}

// TestLoadRejectsMalformedTOML 覆盖配置文件语法错误。
func TestLoadRejectsMalformedTOML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", "[server\nport = ")

	if _, err := Load(path, ""); err == nil {
		t.Fatal("非法 TOML 必须报错")
	}
}

// TestDurationHelpers 覆盖时长换算。
func TestDurationHelpers(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "")
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}

	if got := cfg.Server.ReadTimeout().Seconds(); got != float64(cfg.Server.ReadTimeoutSeconds) {
		t.Errorf("ReadTimeout = %v，与配置不一致", got)
	}
	if got := cfg.Security.SessionTTL().Seconds(); got != float64(cfg.Security.SessionTTLSeconds) {
		t.Errorf("SessionTTL = %v，与配置不一致", got)
	}
	if got := cfg.Database.ConnectTimeout().Seconds(); got != float64(cfg.Database.ConnectTimeoutSeconds) {
		t.Errorf("ConnectTimeout = %v，与配置不一致", got)
	}
	if got := cfg.Server.ShutdownTimeout().Seconds(); got != float64(cfg.Server.ShutdownTimeoutSeconds) {
		t.Errorf("ShutdownTimeout = %v，与配置不一致", got)
	}
	if got := cfg.Redis.RedisDialTimeout().Seconds(); got != float64(cfg.Redis.DialTimeoutSeconds) {
		t.Errorf("RedisDialTimeout = %v，与配置不一致", got)
	}
	if got := cfg.Database.MaxConnLifetime().Seconds(); got != float64(cfg.Database.MaxConnLifetimeSeconds) {
		t.Errorf("MaxConnLifetime = %v，与配置不一致", got)
	}
}

// TestEveryEnvBindingIsUnique 防止绑定表出现重复键（后写会静默覆盖先写）。
func TestEveryEnvBindingIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, binding := range envBindings() {
		if seen[binding.key] {
			t.Errorf("环境变量绑定重复: %s", binding.key)
		}
		seen[binding.key] = true
		if binding.apply == nil {
			t.Errorf("环境变量 %s 缺少 apply 函数", binding.key)
		}
	}
}
