# 端到端冒烟检查：健康检查 + 注册 / 登录 / 当前用户 / 登出。
#
# 用途：在真实依赖上验证"服务能启动且 Auth 链路可用"，作为端到端验收的最小证据。
# 前提：服务已启动（make run-test-config 或 make run）。
#
# 用法：
#   pwsh -File ./scripts/smoke.ps1
#   pwsh -File ./scripts/smoke.ps1 -BaseUrl http://127.0.0.1:8001

[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8000",
    # 每次运行使用唯一用户名，避免重复注册导致 409。
    [string]$Username = "smoke_$([DateTime]::UtcNow.ToString('yyyyMMddHHmmss'))"
)

$ErrorActionPreference = 'Stop'
$script:Failures = 0
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession

function Write-Step {
    param([string]$Message)
    Write-Host "`n=== $Message ===" -ForegroundColor Cyan
}

function Assert-Equal {
    param($Actual, $Expected, [string]$Label)
    if ($Actual -eq $Expected) {
        Write-Host ("  [PASS] {0} = {1}" -f $Label, $Actual) -ForegroundColor Green
    } else {
        Write-Host ("  [FAIL] {0} = {1}，期望 {2}" -f $Label, $Actual, $Expected) -ForegroundColor Red
        $script:Failures++
    }
}

# Invoke-Api 返回状态码与解析后的响应体，不因非 2xx 抛异常。
function Invoke-Api {
    param(
        [string]$Method,
        [string]$Path,
        $Body,
        [hashtable]$Headers
    )

    $params = @{
        Method      = $Method
        Uri         = "$BaseUrl$Path"
        WebSession  = $session
        ErrorAction = 'Stop'
    }
    if ($Headers) { $params.Headers = $Headers }
    if ($null -ne $Body) {
        $params.ContentType = 'application/json'
        $params.Body = ($Body | ConvertTo-Json -Compress)
    }

    try {
        $response = Invoke-WebRequest @params
        return [pscustomobject]@{
            Status = [int]$response.StatusCode
            Body   = if ($response.Content) { $response.Content | ConvertFrom-Json } else { $null }
            SetCookie = $response.Headers['Set-Cookie']
        }
    } catch {
        $status = $_.Exception.Response.StatusCode.value__
        $content = $null
        if ($_.ErrorDetails.Message) {
            try { $content = $_.ErrorDetails.Message | ConvertFrom-Json } catch { }
        }
        return [pscustomobject]@{
            Status    = $status
            Body      = $content
            SetCookie = $null
        }
    }
}

Write-Host "冒烟检查目标：$BaseUrl" -ForegroundColor Yellow

# ---------------------------------------------------------------------------
Write-Step "健康检查"
# ---------------------------------------------------------------------------

# liveness 不依赖外部资源。
$live = Invoke-Api -Method GET -Path '/api/v1/health/live'
Assert-Equal $live.Status 200 'GET /api/v1/health/live'

# check 要求 arg 头：缺少时应被拒绝而不是返回 200。
$checkMissing = Invoke-Api -Method GET -Path '/api/v1/health/check'
if ($checkMissing.Status -ne 200) {
    Write-Host ("  [PASS] 缺少 arg 头被拒绝：{0}" -f $checkMissing.Status) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 缺少 arg 头时不应返回 200" -ForegroundColor Red
    $script:Failures++
}
$checkOk = Invoke-Api -Method GET -Path '/api/v1/health/check' -Headers @{ arg = 'smoke' }
Assert-Equal $checkOk.Status 200 'GET /api/v1/health/check (带 arg 头)'

# readiness 检查数据库与 Redis。
$ready = Invoke-Api -Method GET -Path '/api/v1/health/ready'
Assert-Equal $ready.Status 200 'GET /api/v1/health/ready'

# ---------------------------------------------------------------------------
Write-Step "会话网关：未登录访问受保护端点"
# ---------------------------------------------------------------------------

$unauthorized = Invoke-Api -Method GET -Path '/api/v1/auth/me'
Assert-Equal $unauthorized.Status 401 'GET /api/v1/auth/me（无 Cookie）'
Assert-Equal $unauthorized.Body.error.code 'authentication_required' 'error.code'
if ($unauthorized.Body.error.request_id) {
    Write-Host ("  [PASS] 错误信封包含 request_id = {0}" -f $unauthorized.Body.error.request_id) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 错误信封缺少 request_id" -ForegroundColor Red
    $script:Failures++
}

# ---------------------------------------------------------------------------
Write-Step "注册"
# ---------------------------------------------------------------------------

$registerBody = @{
    username     = $Username
    email        = "$Username@example.com"
    display_name = "冒烟用户"
    password     = 'correct horse battery'
    timezone     = 'Asia/Shanghai'
}
$register = Invoke-Api -Method POST -Path '/api/v1/auth/register' -Body $registerBody
Assert-Equal $register.Status 201 'POST /api/v1/auth/register'
Assert-Equal $register.Body.data.username $Username 'data.username'

$userId = $register.Body.data.id
if ($userId) {
    Write-Host ("  [PASS] 返回用户 id = {0}" -f $userId) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 注册响应缺少 id（契约 2.1）" -ForegroundColor Red
    $script:Failures++
}
if ($register.Body.data.updated_at) {
    Write-Host ("  [PASS] 返回 updated_at = {0}" -f $register.Body.data.updated_at) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 注册响应缺少 updated_at（契约 2.1）" -ForegroundColor Red
    $script:Failures++
}

# 摘要与密码绝不能出现在响应里。
$rawResponse = $register.Body | ConvertTo-Json -Depth 10
foreach ($leak in @('password_hash', 'correct horse battery', '$argon2id$')) {
    if ($rawResponse -like "*$leak*") {
        Write-Host ("  [FAIL] 注册响应泄漏敏感内容：{0}" -f $leak) -ForegroundColor Red
        $script:Failures++
    }
}
Write-Host "  [PASS] 响应未泄漏密码或摘要" -ForegroundColor Green

# ---------------------------------------------------------------------------
Write-Step "重复注册应 409"
# ---------------------------------------------------------------------------

$duplicate = Invoke-Api -Method POST -Path '/api/v1/auth/register' -Body $registerBody
Assert-Equal $duplicate.Status 409 '重复注册'
Assert-Equal $duplicate.Body.error.code 'conflict' 'error.code'

# ---------------------------------------------------------------------------
Write-Step "字段校验应 422"
# ---------------------------------------------------------------------------

$invalid = Invoke-Api -Method POST -Path '/api/v1/auth/register' -Body @{
    username = 'ab'
    email    = 'not-an-email'
    password = 'short'
}
Assert-Equal $invalid.Status 422 '非法字段注册'
Assert-Equal $invalid.Body.error.code 'validation_error' 'error.code'
if ($invalid.Body.error.fields) {
    Write-Host ("  [PASS] 返回字段明细：{0}" -f (($invalid.Body.error.fields | Get-Member -MemberType NoteProperty).Name -join ', ')) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 校验错误缺少 fields 明细" -ForegroundColor Red
    $script:Failures++
}

# ---------------------------------------------------------------------------
Write-Step "登录（凭据错误）"
# ---------------------------------------------------------------------------

$badLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $Username
    password   = 'wrong password'
}
Assert-Equal $badLogin.Status 401 '错误密码登录'
Assert-Equal $badLogin.Body.error.code 'invalid_credentials' 'error.code'

# ---------------------------------------------------------------------------
Write-Step "登录（成功）"
# ---------------------------------------------------------------------------

$login = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $Username
    password   = 'correct horse battery'
}
Assert-Equal $login.Status 200 'POST /api/v1/auth/login'

$cookieSet = $login.SetCookie
if ($cookieSet -and ($cookieSet -join ';') -match 'HttpOnly') {
    Write-Host "  [PASS] 会话 Cookie 带 HttpOnly" -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 会话 Cookie 缺少 HttpOnly" -ForegroundColor Red
    $script:Failures++
}

# ---------------------------------------------------------------------------
Write-Step "当前用户（已登录）"
# ---------------------------------------------------------------------------

$me = Invoke-Api -Method GET -Path '/api/v1/auth/me'
Assert-Equal $me.Status 200 'GET /api/v1/auth/me（已登录）'
Assert-Equal $me.Body.data.id $userId 'data.id 与注册一致'

# 登录必须记录最后登录时间。
if ($me.Body.data.last_login_at) {
    Write-Host ("  [PASS] last_login_at = {0}" -f $me.Body.data.last_login_at) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 登录后 last_login_at 必须非空" -ForegroundColor Red
    $script:Failures++
}

# ---------------------------------------------------------------------------
Write-Step "登出"
# ---------------------------------------------------------------------------

$logout = Invoke-Api -Method POST -Path '/api/v1/auth/logout'
Assert-Equal $logout.Status 204 'POST /api/v1/auth/logout'

# 登出后重新访问：会话已在 Redis 中撤销，Cookie 也已清除。
$afterLogout = Invoke-Api -Method GET -Path '/api/v1/auth/me'
Assert-Equal $afterLogout.Status 401 'GET /api/v1/auth/me（登出后）'

# ---------------------------------------------------------------------------
Write-Step "结果"
# ---------------------------------------------------------------------------

if ($script:Failures -eq 0) {
    Write-Host "全部冒烟检查通过" -ForegroundColor Green
    exit 0
}

Write-Host ("存在 {0} 项失败" -f $script:Failures) -ForegroundColor Red
exit 1
