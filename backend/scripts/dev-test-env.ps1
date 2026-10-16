# 测试环境一键启动：依赖容器 → 迁移 → 启动 API（可选：同时起前端 dev server）。
#
# 用途：一条命令把"能联调的后端"拉起来，方便对照前端的实际状态。
#       等价于 Makefile 的 deps-up + migrate-test-up + run-test-config，但
#       ① 额外等待依赖健康、打印可用地址与测试账号；② 不要求装 make；
#       ③ 带 -Stop 收尾，避免残留监听端口的进程。
#
# ★ 不碰任何测试或冒烟数据 ★
#   - 只做 `docker compose up -d`（保留数据卷）、`migrate up`（幂等）、启动 API；
#   - **不执行** go test、**不执行** scripts/smoke.ps1、**不执行** deps-reset；
#   - 引导管理员只在"不存在"时创建，绝不覆盖既有密码（backend/docs/B03 §3.6.1）。
#   反过来要注意：`make test` 的集成测试会 TRUNCATE 业务表（连引导管理员一起删），
#   跑过测试后要重启本脚本起的服务，否则登录会失败（B03 §7.2）。
#
# 用法（在 backend/ 目录下执行）：
#   pwsh -File ./scripts/dev-test-env.ps1                  # 依赖 + 迁移 + API（前台，Ctrl+C 结束）
#   pwsh -File ./scripts/dev-test-env.ps1 -WithFrontend     # 再开一个窗口起前端（pnpm/npm run dev）
#   pwsh -File ./scripts/dev-test-env.ps1 -SkipDeps         # 依赖已在跑：跳过 docker 步骤
#   pwsh -File ./scripts/dev-test-env.ps1 -MigrateOnly      # 只准备依赖与迁移，不起服务
#   pwsh -File ./scripts/dev-test-env.ps1 -Stop             # 停掉 API（按端口）并停止依赖容器

[CmdletBinding()]
param(
    # 停掉 API 与依赖容器后退出
    [switch]$Stop,
    # 依赖容器已经在跑时跳过 docker 步骤
    [switch]$SkipDeps,
    # 只准备依赖与迁移，不启动 API
    [switch]$MigrateOnly,
    # 另外开一个窗口启动前端 dev server
    [switch]$WithFrontend,
    # API 监听端口（用于 -Stop 与健康检查；服务端口由 config.toml / SERVER_PORT 决定）
    [int]$Port = 8000,
    # 前端工程目录（默认 ../frontend）
    [string]$FrontendPath = (Join-Path $PSScriptRoot '..\..\frontend'),
    # 等待依赖容器健康的超时（秒）
    [int]$HealthTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'

# ---- 与 Makefile 的 TEST_* 常量保持一致（docker/test.env + config.toml）----
$BackendRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$TestEnv = 'docker/test.env'
$TestCompose = 'docker/compose.test.yml'
$TestDatabaseUrl = 'postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable'
$TestRedisPassword = 'project_builder_test_redis_password'
$TestRedisPort = '16379'
$BootstrapAdminUsername = 'bootstrap_admin'
$BootstrapAdminEmail = 'bootstrap_admin@test.local'
$BootstrapAdminPassword = 'bootstrap_admin_test_password'
$Services = @('postgres', 'redis', 'mailpit')

function Write-Step {
    param([string]$Message)
    Write-Host "`n=== $Message ===" -ForegroundColor Cyan
}

function Write-Info {
    param([string]$Message)
    Write-Host "  $Message"
}

function Assert-Command {
    param([string]$Name, [string]$Hint)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "找不到命令 '$Name'。$Hint"
    }
}

function Invoke-Compose {
    param([string[]]$ComposeArgs)
    Push-Location $BackendRoot
    try {
        & docker compose --env-file $TestEnv -f $TestCompose @ComposeArgs
        if ($LASTEXITCODE -ne 0) {
            throw "docker compose $($ComposeArgs -join ' ') 失败（exit $LASTEXITCODE）"
        }
    } finally {
        Pop-Location
    }
}

function Wait-ServiceHealthy {
    param([string]$Service)
    $deadline = (Get-Date).AddSeconds($HealthTimeoutSeconds)
    while ($true) {
        Push-Location $BackendRoot
        try {
            $id = (& docker compose --env-file $TestEnv -f $TestCompose ps -q $Service 2>$null | Select-Object -First 1)
        } finally {
            Pop-Location
        }
        if ($id) {
            $status = (& docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $id 2>$null)
            if ($status -eq 'healthy' -or $status -eq 'running') {
                Write-Host ("  [OK] {0,-9} {1}" -f $Service, $status) -ForegroundColor Green
                return
            }
        }
        if ((Get-Date) -gt $deadline) {
            throw "等待 $Service 健康超时（$HealthTimeoutSeconds 秒）。可用 docker compose --env-file $TestEnv -f $TestCompose logs $Service 查看原因。"
        }
        Start-Sleep -Seconds 2
    }
}

function Get-ApiProcess {
    $connection = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $connection) {
        return $null
    }
    return Get-Process -Id $connection.OwningProcess -ErrorAction SilentlyContinue
}

function Start-Frontend {
    $frontendRoot = (Resolve-Path $FrontendPath -ErrorAction SilentlyContinue)
    if (-not $frontendRoot) {
        Write-Host "  跳过前端：找不到目录 $FrontendPath" -ForegroundColor Yellow
        return
    }
    # 本机可能没有 pnpm（它不在 PATH 上），npm 一样能跑 package.json 里的脚本
    $runner = if (Get-Command pnpm -ErrorAction SilentlyContinue) { 'pnpm' } else { 'npm' }
    Write-Host "  启动前端：$runner run dev（新窗口，目录 $($frontendRoot.Path)）" -ForegroundColor Green
    Start-Process -FilePath 'pwsh' `
        -ArgumentList @('-NoExit', '-Command', "$runner run dev") `
        -WorkingDirectory $frontendRoot.Path | Out-Null
}

# ---- -Stop：先停 API，再停依赖容器 ----
if ($Stop) {
    Write-Step '停止 API'
    $api = Get-ApiProcess
    if ($api) {
        Write-Info "结束进程 $($api.Id)（$($api.ProcessName)），它占用了端口 $Port"
        Stop-Process -Id $api.Id -Force
        Start-Sleep -Seconds 2
    } else {
        Write-Info "端口 $Port 上没有监听进程"
    }
    if (-not $SkipDeps) {
        Assert-Command -Name 'docker' -Hint '停止依赖容器需要 docker；只要停 API 可加 -SkipDeps。'
        Write-Step '停止依赖容器（保留数据卷）'
        Invoke-Compose @('stop')
        Write-Info '数据卷仍然保留；下次启动会复用同一份测试数据'
    }
    Write-Host "`n完成。" -ForegroundColor Green
    exit 0
}

Assert-Command -Name 'docker' -Hint '本脚本需要 Docker Desktop（或等价的 docker + compose）。'
Assert-Command -Name 'go' -Hint '本脚本用 go run 启动服务；请把 Go 加到 PATH（B03 §7.0.1）。'

# ---- 1. 依赖容器 ----
if ($SkipDeps) {
    Write-Step '依赖容器（-SkipDeps：跳过）'
    Write-Info '假定 PostgreSQL 15432 / Redis 16379 已经在跑'
} else {
    Write-Step '启动依赖容器'
    Invoke-Compose @('up', '-d')
    foreach ($service in $Services) {
        Wait-ServiceHealthy -Service $service
    }
}

# ---- 2. 迁移 ----
Write-Step '应用数据库迁移'
$env:DATABASE_URL = $TestDatabaseUrl
Push-Location $BackendRoot
try {
    & go run ./cmd/migrate up
    if ($LASTEXITCODE -ne 0) { throw "迁移失败（exit $LASTEXITCODE）" }
    & go run ./cmd/migrate version
    if ($LASTEXITCODE -ne 0) { throw "读取迁移版本失败（exit $LASTEXITCODE）" }
} finally {
    Pop-Location
}

if ($MigrateOnly) {
    Write-Host "`n依赖与迁移已就绪（-MigrateOnly，未启动 API）。" -ForegroundColor Green
    Write-Info '启动服务：pwsh -File ./scripts/dev-test-env.ps1 -SkipDeps'
    exit 0
}

# ---- 3. 启动 API（与 make run-test-config 相同的环境变量）----
if (Get-ApiProcess) {
    Write-Host "`n端口 $Port 已被占用：请先用 -Stop 停掉旧进程，或改用 -Port 指定别的端口。" -ForegroundColor Yellow
    exit 1
}

$env:REDIS_HOST = '127.0.0.1'
$env:REDIS_PORT = $TestRedisPort
$env:REDIS_PASSWORD = $TestRedisPassword
$env:SECURITY_SESSION_SECRET = 'local-dev-session-secret-change-me'
$env:SECURITY_BOOTSTRAP_ADMIN_USERNAME = $BootstrapAdminUsername
$env:SECURITY_BOOTSTRAP_ADMIN_EMAIL = $BootstrapAdminEmail
$env:SECURITY_BOOTSTRAP_ADMIN_PASSWORD = $BootstrapAdminPassword
# 冒烟脚本对"调度器已生成提醒"只轮询 30 秒，因此这个值必须与 make run-test-config 一致
$env:SCHEDULER_INTERVAL_SECONDS = '5'

if ($WithFrontend) {
    Write-Step '启动前端 dev server'
    Start-Frontend
}

Write-Step '启动 API'
Write-Host @"
  后端地址   http://127.0.0.1:$Port     健康检查  http://127.0.0.1:$Port/healthz
  前端地址   http://localhost:5173      （前端命令在 frontend/ 下：pnpm dev 或 npm run dev）
  测试账号   $BootstrapAdminUsername / $BootstrapAdminPassword     仅测试环境使用
  邮件面板   http://127.0.0.1:18025     （Mailpit，查看提醒邮件）
  停止服务   Ctrl+C 结束 API；再执行 pwsh -File ./scripts/dev-test-env.ps1 -Stop 停依赖容器

  提醒：本脚本不跑测试也不跑冒烟。`make test` 会 TRUNCATE 业务表（含引导管理员），
        跑完测试后要重启本服务，登录才恢复正常（B03 §7.2）。
"@ -ForegroundColor DarkGray

Push-Location $BackendRoot
try {
    & go run ./cmd/api
} finally {
    Pop-Location
}
