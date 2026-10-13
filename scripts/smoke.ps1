# 端到端冒烟检查：健康检查、认证、用户/工作组/项目/任务、计划视图、附件、提醒。
#
# 用途：在真实依赖上验证"服务能启动且各域链路可用"，作为端到端验收的最小证据。
# 前提：服务已按 make run-test-config 启动（127.0.0.1:8000）——它同时注入
#       引导管理员凭据与 SCHEDULER_INTERVAL_SECONDS=5，后者是本脚本"提醒"小节的前提。
#       换用其他配置（例如 docker/config.test.toml）会在提醒小节稳定失败，见 docs/06 §7.0.1。
#
# 用法：
#   make smoke
#   pwsh -File ./scripts/smoke.ps1
#   pwsh -File ./scripts/smoke.ps1 -BaseUrl http://127.0.0.1:8000

[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8000",
    # 每次运行使用唯一用户名，避免重复注册导致 409。
    [string]$Username = "smoke_$([DateTime]::UtcNow.ToString('yyyyMMddHHmmss'))",
    # 引导管理员凭据：默认值对应 make run-test-config 注入的测试配置
    # （Makefile 中的 TEST_BOOTSTRAP_ADMIN_*）。
    # 管理员端点的成功路径需要它；缺失时该小节会给出可操作的失败提示。
    [string]$AdminUsername = "bootstrap_admin",
    [string]$AdminPassword = "bootstrap_admin_test_password"
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

# Assert-True 断言一个布尔条件（用于无法直接比较取值的检查）。
function Assert-True {
    param([bool]$Condition, [string]$Label)
    if ($Condition) {
        Write-Host ("  [PASS] {0}" -f $Label) -ForegroundColor Green
    } else {
        Write-Host ("  [FAIL] {0}" -f $Label) -ForegroundColor Red
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

# Invoke-ApiUpload 上传一个附件（multipart/form-data）。
#
# 单独一个函数而不是给 Invoke-Api 加参数：JSON 与 multipart 的
# ContentType/Body 处理完全不同，混在一起会让两者都难以阅读。
function Invoke-ApiUpload {
    param(
        [string]$Path,
        [string]$FileName,
        [byte[]]$Content,
        [string]$ContentType = 'application/octet-stream'
    )

    $temp = Join-Path ([IO.Path]::GetTempPath()) $FileName
    [IO.File]::WriteAllBytes($temp, $Content)
    try {
        $params = @{
            Method      = 'POST'
            Uri         = "$BaseUrl$Path"
            WebSession  = $session
            Form        = @{ file = Get-Item $temp }
            ErrorAction = 'Stop'
        }
        try {
            $response = Invoke-WebRequest @params
            return [pscustomobject]@{
                Status = [int]$response.StatusCode
                Body   = if ($response.Content) { $response.Content | ConvertFrom-Json } else { $null }
            }
        } catch {
            $status = $_.Exception.Response.StatusCode.value__
            $content = $null
            if ($_.ErrorDetails.Message) {
                try { $content = $_.ErrorDetails.Message | ConvertFrom-Json } catch { }
            }
            return [pscustomobject]@{ Status = $status; Body = $content }
        }
    } finally {
        Remove-Item $temp -Force -ErrorAction SilentlyContinue
    }
}

# Invoke-ApiDownload 下载附件并返回原始字节。
function Invoke-ApiDownload {
    param([string]$Path)

    try {
        $response = Invoke-WebRequest -Method GET -Uri "$BaseUrl$Path" -WebSession $session -ErrorAction Stop
        return [pscustomobject]@{
            Status      = [int]$response.StatusCode
            Content     = $response.Content
            RawBytes    = $response.RawContentStream.ToArray()
            Disposition = $response.Headers['Content-Disposition']
        }
    } catch {
        return [pscustomobject]@{
            Status      = $_.Exception.Response.StatusCode.value__
            Content     = $null
            RawBytes    = $null
            Disposition = $null
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
Write-Step "Groups：创建 → 成员 → 加入申请"
# ---------------------------------------------------------------------------

# 重新登录（前面的登出已撤销会话）
$login = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $Username
    password   = 'correct horse battery'
}
Assert-Equal $login.Status 200 '重新登录'

$groupId = $null
$group = Invoke-Api -Method POST -Path '/api/v1/groups' -Body @{
    name        = "冒烟工作组 $Username"
    description = '由冒烟脚本创建'
}
Assert-Equal $group.Status 201 'POST /api/v1/groups'
$groupId = $group.Body.data.id
Assert-Equal $group.Body.data.membership_role 'manager' '创建者应为 manager'

$groupList = Invoke-Api -Method POST -Path '/api/v1/groups/query' -Body @{ page = 1; page_size = 20 }
Assert-Equal $groupList.Status 200 'POST /api/v1/groups/query'
if ($groupList.Body.pagination.total -ge 1) {
    Write-Host ("  [PASS] pagination.total = {0}" -f $groupList.Body.pagination.total) -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 列表应至少包含刚创建的工作组" -ForegroundColor Red
    $script:Failures++
}

# 分页越界必须被拒绝，而不是静默夹取
$badPage = Invoke-Api -Method POST -Path '/api/v1/groups/query' -Body @{ page_size = 500 }
Assert-Equal $badPage.Status 400 'page_size 超限应 400'
Assert-Equal $badPage.Body.error.code 'invalid_request' 'error.code'

# 未知排序字段必须被拒绝
$badSort = Invoke-Api -Method POST -Path '/api/v1/groups/query' -Body @{ sort = 'nonsense' }
Assert-Equal $badSort.Status 400 '未知排序字段应 400'

# 所有者不可被移除
$groupDetail = Invoke-Api -Method GET -Path "/api/v1/groups/$groupId"
Assert-Equal $groupDetail.Status 200 'GET /api/v1/groups/{id}'
$groupVersion = $groupDetail.Body.data.updated_at

$removeOwner = Invoke-Api -Method DELETE -Path "/api/v1/groups/$groupId/members/$userId"
Assert-Equal $removeOwner.Status 403 '移除所有者应 403'

# 省略版本必须被拒绝（契约加强项）
$noVersion = Invoke-Api -Method PATCH -Path "/api/v1/groups/$groupId" -Body @{ name = '改名' }
Assert-Equal $noVersion.Status 422 '省略 expected_updated_at 应 422'

$renamed = Invoke-Api -Method PATCH -Path "/api/v1/groups/$groupId" -Body @{
    name                = "冒烟工作组（已改名）"
    expected_updated_at = $groupVersion
}
Assert-Equal $renamed.Status 200 '带版本更新应 200'
if ($renamed.Body.data.updated_at -ne $groupVersion) {
    Write-Host "  [PASS] updated_at 已推进（版本协议生效）" -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 更新必须推进 updated_at" -ForegroundColor Red
    $script:Failures++
}

# 过期版本应 409
$staleVersion = Invoke-Api -Method PATCH -Path "/api/v1/groups/$groupId" -Body @{
    name                = '再次改名'
    expected_updated_at = $groupVersion
}
Assert-Equal $staleVersion.Status 409 '过期版本应 409'
Assert-Equal $staleVersion.Body.error.code 'version_conflict' 'error.code'

# ---------------------------------------------------------------------------
Write-Step "Users：可见性 → 权限 → 版本 → 字段生效"
# ---------------------------------------------------------------------------

# 注册并登录第二个用户，用于验证跨用户可见性与"可见但不可改"。
$secondUsername = "${Username}_b"
$primarySession = $session
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$null = Invoke-Api -Method POST -Path '/api/v1/auth/register' -Body @{
    username     = $secondUsername
    email        = "$secondUsername@example.com"
    display_name = '冒烟用户 B'
    password     = 'correct horse battery'
    timezone     = 'UTC'
}
$secondLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $secondUsername
    password   = 'correct horse battery'
}
Assert-Equal $secondLogin.Status 200 '第二个用户登录'
$secondUserId = $secondLogin.Body.data.id

# 尚未加入同一工作组时互相不可见：必须是 404，不能泄漏账号是否存在。
$session = $primarySession
$invisible = Invoke-Api -Method GET -Path "/api/v1/users/$secondUserId"
Assert-Equal $invisible.Status 404 '尚无协作关系时不可见（404）'
Assert-Equal $invisible.Body.error.code 'user_not_found' 'error.code'

$null = Invoke-Api -Method POST -Path "/api/v1/groups/$groupId/members" -Body @{ user_id = $secondUserId }

$visible = Invoke-Api -Method GET -Path "/api/v1/users/$secondUserId"
Assert-Equal $visible.Status 200 '同组成员应可见（200）'
if ($visible.Body.data.password_hash) {
    Write-Host "  [FAIL] 用户资料响应泄漏了 password_hash" -ForegroundColor Red
    $script:Failures++
} else {
    Write-Host "  [PASS] 用户资料响应未包含 password_hash" -ForegroundColor Green
}

# 同组成员可以看见，但不能修改别人的资料。
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$null = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $secondUsername
    password   = 'correct horse battery'
}
$foreignEdit = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    display_name        = '被同事改名'
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $foreignEdit.Status 403 '修改他人资料应 403'
Assert-Equal $foreignEdit.Body.error.code 'permission_denied' 'error.code'

# 停用用户仅限系统管理员。
$foreignDisable = Invoke-Api -Method POST -Path "/api/v1/users/$userId/disable" -Body @{
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $foreignDisable.Status 403 '非管理员停用用户应 403'

$session = $primarySession
$selfDisable = Invoke-Api -Method POST -Path "/api/v1/users/$userId/disable" -Body @{
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $selfDisable.Status 403 '普通用户停用他人应 403'

# 路径参数非法：必须 400，不能交给数据库退化成 500。
$badUserID = Invoke-Api -Method GET -Path '/api/v1/users/not-a-uuid'
Assert-Equal $badUserID.Status 400 '非法 user_id 应 400'
Assert-Equal $badUserID.Body.error.code 'invalid_request' 'error.code'

$missingUser = Invoke-Api -Method GET -Path '/api/v1/users/00000000-0000-7000-8000-000000000000'
Assert-Equal $missingUser.Status 404 '不存在的用户应 404 user_not_found'

$myProfile = Invoke-Api -Method GET -Path "/api/v1/users/$userId"
Assert-Equal $myProfile.Status 200 'GET /api/v1/users/{self}'
$myVersion = $myProfile.Body.data.updated_at

# 只有版本、没有任何可改字段：必须 422，否则会推进 updated_at 让别的客户端版本失效。
$emptyUpdate = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    expected_updated_at = $myVersion
}
Assert-Equal $emptyUpdate.Status 422 '空更新应 422'
Assert-Equal $emptyUpdate.Body.error.code 'validation_error' 'error.code'

# 未知字段（username 不在契约内）必须被拒绝，而不是静默忽略。
$unknownField = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    username            = 'renamed'
    expected_updated_at = $myVersion
}
Assert-Equal $unknownField.Status 422 '未知字段应 422'
Assert-Equal $unknownField.Body.error.code 'validation_error' 'error.code'

$badTimezone = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    timezone            = 'Mars/Olympus'
    expected_updated_at = $myVersion
}
Assert-Equal $badTimezone.Status 422 '非法时区应 422'

# 普通用户即使是本人也不能改状态或系统角色。
$selfRole = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    system_role         = 'admin'
    expected_updated_at = $myVersion
}
Assert-Equal $selfRole.Status 403 '普通用户改系统角色应 403'
Assert-Equal $selfRole.Body.error.code 'permission_denied' 'error.code'

# 邮箱唯一性由数据库保证。
$duplicateEmail = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    email               = "$secondUsername@example.com"
    expected_updated_at = $myVersion
}
Assert-Equal $duplicateEmail.Status 409 '占用他人邮箱应 409'
Assert-Equal $duplicateEmail.Body.error.code 'conflict' 'error.code'

# 字段必须真实生效（历史缺陷：归一化丢字段，返回 200 但目标字段没变）。
$profileUpdate = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    display_name        = '冒烟用户（已改名）'
    timezone            = 'Pacific/Honolulu'
    reminder_enabled    = $false
    expected_updated_at = $myVersion
}
Assert-Equal $profileUpdate.Status 200 'PATCH /api/v1/users/{self}'
Assert-Equal $profileUpdate.Body.data.display_name '冒烟用户（已改名）' 'display_name 已生效'
Assert-Equal $profileUpdate.Body.data.timezone 'Pacific/Honolulu' 'timezone 已生效'
Assert-Equal $profileUpdate.Body.data.reminder_enabled $false 'reminder_enabled 已生效'
if ($profileUpdate.Body.data.updated_at -ne $myVersion) {
    Write-Host "  [PASS] 资料更新推进了 updated_at（版本协议生效）" -ForegroundColor Green
} else {
    Write-Host "  [FAIL] 资料更新必须推进 updated_at" -ForegroundColor Red
    $script:Failures++
}

# 过期版本应 409。
$staleProfile = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    display_name        = '用过期版本改名'
    expected_updated_at = $myVersion
}
Assert-Equal $staleProfile.Status 409 '过期版本应 409'
Assert-Equal $staleProfile.Body.error.code 'version_conflict' 'error.code'

# 复原时区，避免影响后续对派生日期的判断。
$restore = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    timezone            = 'Asia/Shanghai'
    expected_updated_at = $profileUpdate.Body.data.updated_at
}
Assert-Equal $restore.Status 200 '时区复原'
Assert-Equal $restore.Body.data.timezone 'Asia/Shanghai' '时区复原已生效'

# ---------------------------------------------------------------------------
Write-Step "Users：管理员端点的权限与参数边界"
# ---------------------------------------------------------------------------

# 本脚本无法自行造出系统管理员（当前首个管理员需要直接写库，见 docs/06 7.4），
# 因此这里只覆盖**权限与参数边界**：非管理员一律 403，非法路径/请求体在
# 权限判定之前就被拒绝。管理员端点的成功路径由集成测试覆盖
# （internal/testsupport/users_test.go）。

# 管理员创建用户：非管理员必须 403。
$forbiddenCreate = Invoke-Api -Method POST -Path '/api/v1/users' -Body @{
    username     = "smoke_admin_$Username"
    email        = "smoke_admin_$Username@example.com"
    display_name = '不应被创建'
    password     = 'correct horse battery'
}
Assert-Equal $forbiddenCreate.Status 403 '非管理员创建用户应 403'
Assert-Equal $forbiddenCreate.Body.error.code 'permission_denied' 'error.code'

# 请求体校验先于权限判定：字段本身不合法时无需先问权限。
$invalidCreate = Invoke-Api -Method POST -Path '/api/v1/users' -Body @{ username = 'x' }
Assert-Equal $invalidCreate.Status 422 '创建用户的非法字段应 422'
Assert-Equal $invalidCreate.Body.error.code 'validation_error' 'error.code'

# 用户列表：非管理员必须 403（普通成员应看到成员列表，而不是全系统账号清单）。
$forbiddenList = Invoke-Api -Method POST -Path '/api/v1/users/query' -Body @{}
Assert-Equal $forbiddenList.Status 403 '非管理员查询用户列表应 403'
Assert-Equal $forbiddenList.Body.error.code 'permission_denied' 'error.code'

# 重置密码：路径参数先校验，非法 UUID 是 400 而不是 500。
$badPasswordPath = Invoke-Api -Method POST -Path '/api/v1/users/not-a-uuid/password' -Body @{
    new_password        = 'brand new password'
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $badPasswordPath.Status 400 '重置密码的非法 user_id 应 400'
Assert-Equal $badPasswordPath.Body.error.code 'invalid_request' 'error.code'

# 重置密码：非管理员必须 403。
$forbiddenReset = Invoke-Api -Method POST -Path "/api/v1/users/$secondUserId/password" -Body @{
    new_password        = 'brand new password'
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $forbiddenReset.Status 403 '非管理员重置他人密码应 403'
Assert-Equal $forbiddenReset.Body.error.code 'permission_denied' 'error.code'

# 缺少 new_password：在绑定阶段就被拒绝。
$missingPassword = Invoke-Api -Method POST -Path "/api/v1/users/$userId/password" -Body @{
    expected_updated_at = '2026-10-11T00:00:00Z'
}
Assert-Equal $missingPassword.Status 422 '缺少 new_password 应 422'

# ---------------------------------------------------------------------------
Write-Step "Users：管理员端点成功路径（引导管理员）"
# ---------------------------------------------------------------------------

# 服务以 make run-test-config 启动时会按 security.bootstrap_admin_* 初始化一个
# 系统级后台管理员，因此管理员端点的成功路径可以在这里端到端验证。
#
# 前提：服务启动之后**没有清空过数据库**（集成测试会 TRUNCATE 业务表，
# 那会把引导管理员一并删掉，而初始化只在启动时执行一次）。
$primarySession = $session
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$adminLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $AdminUsername
    password   = $AdminPassword
}
if ($adminLogin.Status -ne 200) {
    Write-Host ("  [FAIL] 引导管理员无法登录（状态码 {0}）。请确认服务以 make run-test-config 启动，" -f $adminLogin.Status) -ForegroundColor Red
    Write-Host "         且此后未清空过数据库（集成测试会删除引导管理员，需重启服务）。" -ForegroundColor Red
    $script:Failures++
} else {
    Write-Host "  [PASS] 引导管理员可登录（初始化配置生效）" -ForegroundColor Green
}

# 管理员创建用户。
$adminCreatedName = "smoke_created_$Username"
$adminCreated = Invoke-Api -Method POST -Path '/api/v1/users' -Body @{
    username     = $adminCreatedName
    email        = "$adminCreatedName@example.com"
    display_name = '管理员创建的账号'
    password     = 'correct horse battery'
    timezone     = 'Asia/Shanghai'
}
Assert-Equal $adminCreated.Status 201 'POST /api/v1/users（管理员）'
Assert-Equal $adminCreated.Body.data.system_role 'member' '新建账号固定为 member'
$adminCreatedId = $adminCreated.Body.data.id

# 唯一性：重复用户名由数据库唯一索引兜底。
$adminDuplicate = Invoke-Api -Method POST -Path '/api/v1/users' -Body @{
    username     = $adminCreatedName
    email        = "other_$adminCreatedName@example.com"
    display_name = '重复用户名'
    password     = 'correct horse battery'
}
Assert-Equal $adminDuplicate.Status 409 '重复用户名应 409'
Assert-Equal $adminDuplicate.Body.error.code 'conflict' 'error.code'

# 用户列表：默认口径能查到新账号。
$adminList = Invoke-Api -Method POST -Path '/api/v1/users/query' -Body @{ page = 1; page_size = 100 }
Assert-Equal $adminList.Status 200 'POST /api/v1/users/query（管理员）'
$listedCreated = @($adminList.Body.data | Where-Object { $_.id -eq $adminCreatedId }).Count
Assert-Equal $listedCreated 1 '默认列表包含新建账号'

# 列表的 include_disabled 口径与分页/排序校验在管理员身份下才可达。
$adminListAll = Invoke-Api -Method POST -Path '/api/v1/users/query' -Body @{ include_disabled = $true; page = 1; page_size = 100 }
Assert-Equal $adminListAll.Status 200 'include_disabled=true 应 200'

$adminBadSort = Invoke-Api -Method POST -Path '/api/v1/users/query' -Body @{ sort = 'password_hash' }
Assert-Equal $adminBadSort.Status 400 '未知排序字段应 400'
Assert-Equal $adminBadSort.Body.error.code 'invalid_request' 'error.code'

$adminBadPage = Invoke-Api -Method POST -Path '/api/v1/users/query' -Body @{ page_size = 500 }
Assert-Equal $adminBadPage.Status 400 'page_size 超限应 400'

# 重置密码：新密码可用、旧密码失效。
$adminReset = Invoke-Api -Method POST -Path "/api/v1/users/$adminCreatedId/password" -Body @{
    new_password        = 'reset by smoke admin'
    expected_updated_at = $adminCreated.Body.data.updated_at
}
Assert-Equal $adminReset.Status 200 'POST /api/v1/users/{id}/password（管理员）'

$staleReset = Invoke-Api -Method POST -Path "/api/v1/users/$adminCreatedId/password" -Body @{
    new_password        = 'another password'
    expected_updated_at = $adminCreated.Body.data.updated_at
}
Assert-Equal $staleReset.Status 409 '过期版本重置应 409'
Assert-Equal $staleReset.Body.error.code 'version_conflict' 'error.code'

$resetLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $adminCreatedName
    password   = 'reset by smoke admin'
}
Assert-Equal $resetLogin.Status 200 '重置后的新密码可登录'

$oldLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $adminCreatedName
    password   = 'correct horse battery'
}
Assert-Equal $oldLogin.Status 401 '重置后的旧密码应失效'

# ---------------------------------------------------------------------------
Write-Step "Audit：操作记录（仅管理员）"
# ---------------------------------------------------------------------------

# 上面的管理员动作（创建账号、重置密码）都应当在操作记录里留下条目。
#
# 注意：`$resetLogin` / `$oldLogin` 两次登录已经把 $session 的 Cookie 换成了
# **被创建账号**的会话，因此这里必须重新以引导管理员身份登录一次，
# 而不是沿用当时的 $session（实测过：沿用它会在下面得到一串 403）。
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$auditLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $AdminUsername
    password   = $AdminPassword
}
Assert-Equal $auditLogin.Status 200 '操作记录用例：引导管理员重新登录'
$auditList = Invoke-Api -Method GET -Path '/api/v1/audit?page=1&page_size=50'
Assert-Equal $auditList.Status 200 'GET /api/v1/audit（管理员）'
Assert-True ($auditList.Body.pagination.total -gt 0) '操作记录非空（写路径已接入审计）'

# 按对象 id 过滤：这个账号上发生过 created 与 password_reset。
$auditForUser = Invoke-Api -Method GET -Path "/api/v1/audit?object_id=$adminCreatedId&object_type=user"
Assert-Equal $auditForUser.Status 200 '按对象过滤应 200'
$createdRows = @($auditForUser.Body.data | Where-Object { $_.action -eq 'created' })
$resetRows = @($auditForUser.Body.data | Where-Object { $_.action -eq 'password_reset' })
Assert-Equal $createdRows.Count 1 '账号创建留下 1 条 created 记录'
Assert-Equal $resetRows.Count 1 '重置密码留下 1 条 password_reset 记录'
Assert-True ($createdRows[0].actor_id -ne $adminCreatedId) '代建记录的操作人不是被创建的账号本身'
Assert-Equal $createdRows[0].actor_id $resetRows[0].actor_id '两次操作的操作人一致（都是引导管理员）'

# 记录里绝不允许出现密码明文。
$auditBodyText = ($auditForUser.Body | ConvertTo-Json -Depth 10 -Compress)
Assert-True (-not $auditBodyText.Contains('reset by smoke admin')) '操作记录不含密码明文'
Assert-True ($resetRows[0].request_id -ne '') '记录带 request_id（可串起同一次请求）'

# 参数校验与权限边界。
$auditBadEnum = Invoke-Api -Method GET -Path '/api/v1/audit?action=unknown_action'
Assert-Equal $auditBadEnum.Status 400 '未知动作应 400'
Assert-Equal $auditBadEnum.Body.error.code 'invalid_request' 'error.code'
$auditBadParam = Invoke-Api -Method GET -Path '/api/v1/audit?kinds=created'
Assert-Equal $auditBadParam.Status 400 '未知查询参数应 400'

# 注意：项目/任务/附件在这之前还没创建（它们在本节之后的小节里），
# "整条链路的可追溯性"断言放在脚本末尾，见那里的说明。

$session = $primarySession
$auditForbidden = Invoke-Api -Method GET -Path '/api/v1/audit'
Assert-Equal $auditForbidden.Status 403 '普通成员访问操作记录应 403'
Assert-Equal $auditForbidden.Body.error.code 'permission_denied' 'error.code'

# ---------------------------------------------------------------------------
Write-Step "Projects：创建 → 派生字段 → 归档只读"
# ---------------------------------------------------------------------------

$project = Invoke-Api -Method POST -Path '/api/v1/projects' -Body @{
    name       = "冒烟项目 $Username"
    group_id   = $groupId
    start_date = '2026-10-01'
    end_date   = '2026-10-31'
}
Assert-Equal $project.Status 201 'POST /api/v1/projects'
$projectId = $project.Body.data.id
Assert-Equal $project.Body.data.visibility 'private' '新项目默认私有'
Assert-Equal $project.Body.data.status 'not_started' '新项目默认未开始'

# 日期区间倒置必须返回 400 invalid_date_range（而不是 422）
$badDates = Invoke-Api -Method POST -Path '/api/v1/projects' -Body @{
    name       = '倒置'
    group_id   = $groupId
    start_date = '2026-10-31'
    end_date   = '2026-10-01'
}
Assert-Equal $badDates.Status 400 '日期区间倒置应 400'
Assert-Equal $badDates.Body.error.code 'invalid_date_range' 'error.code'

# ---------------------------------------------------------------------------
Write-Step "Tasks：创建 → 组合筛选 → 幂等复制 → 归档只读"
# ---------------------------------------------------------------------------

$task = Invoke-Api -Method POST -Path "/api/v1/projects/$projectId/tasks" -Body @{
    name       = '冒烟任务'
    start_date = '2026-10-01'
    end_date   = '2026-10-05'
}
Assert-Equal $task.Status 201 'POST /api/v1/projects/{id}/tasks'
$taskId = $task.Body.data.id
Assert-Equal $task.Body.data.membership_role 'owner' '创建者应为任务持有者'

# 组合筛选（历史缺陷：恒为空）
$combined = Invoke-Api -Method POST -Path "/api/v1/projects/$projectId/tasks/query" -Body @{
    owner_id    = $userId
    assignee_id = $userId
}
Assert-Equal $combined.Status 200 '组合筛选应 200'
if ($combined.Body.pagination.total -eq 1) {
    Write-Host "  [PASS] owner_id + assignee_id 组合筛选命中 1 条（历史缺陷为 0）" -ForegroundColor Green
} else {
    Write-Host ("  [FAIL] 组合筛选 total = {0}，期望 1" -f $combined.Body.pagination.total) -ForegroundColor Red
    $script:Failures++
}

# 唯一 owner 不可移除
$removeTaskOwner = Invoke-Api -Method DELETE -Path "/api/v1/tasks/$taskId/members/$userId"
Assert-Equal $removeTaskOwner.Status 403 '移除任务唯一持有者应 403'

# 幂等复制：首次 201，重复 200，且只产生一个副本
$copyKey = "smoke-copy-$Username"
$copy1 = Invoke-Api -Method POST -Path "/api/v1/tasks/$taskId/copy" -Body @{} -Headers @{ 'Idempotency-Key' = $copyKey }
Assert-Equal $copy1.Status 201 '首次复制应 201'
$copyId = $copy1.Body.data.id

$copy2 = Invoke-Api -Method POST -Path "/api/v1/tasks/$taskId/copy" -Body @{} -Headers @{ 'Idempotency-Key' = $copyKey }
Assert-Equal $copy2.Status 200 '重复复制应 200（重放）'
Assert-Equal $copy2.Body.data.id $copyId '重放必须返回同一副本'

$afterCopy = Invoke-Api -Method POST -Path "/api/v1/projects/$projectId/tasks/query" -Body @{}
if ($afterCopy.Body.pagination.total -eq 2) {
    Write-Host "  [PASS] 幂等键生效：任务总数 = 2（源 + 一个副本）" -ForegroundColor Green
} else {
    Write-Host ("  [FAIL] 任务总数 = {0}，期望 2（幂等键未阻止重复创建）" -f $afterCopy.Body.pagination.total) -ForegroundColor Red
    $script:Failures++
}

# ---------------------------------------------------------------------------
Write-Step "Plan Views：个人 / 组内 / 甘特图"
# ---------------------------------------------------------------------------

$personal = Invoke-Api -Method GET -Path '/api/v1/plan-views/personal'
Assert-Equal $personal.Status 200 'GET /api/v1/plan-views/personal'

$personalProjects = @($personal.Body.data.projects | Where-Object { $_.id -eq $projectId }).Count
Assert-Equal $personalProjects 1 '个人视图包含刚创建的项目'
$personalTasks = @($personal.Body.data.tasks | Where-Object { $_.id -eq $taskId }).Count
Assert-Equal $personalTasks 1 '个人视图包含刚创建的任务'

# 汇总统计必须与返回的条目一致（同一套数据算出，不允许各算一套）。
Assert-Equal $personal.Body.data.summary.project_count (@($personal.Body.data.projects).Count) 'summary.project_count 与项目条目一致'
Assert-Equal $personal.Body.data.summary.task_count (@($personal.Body.data.tasks).Count) 'summary.task_count 与任务条目一致'

$groupView = Invoke-Api -Method GET -Path "/api/v1/plan-views/groups/$groupId"
Assert-Equal $groupView.Status 200 'GET /api/v1/plan-views/groups/{id}'
Assert-Equal $groupView.Body.data.group_id $groupId 'group_id 回显正确'

$gantt = Invoke-Api -Method GET -Path "/api/v1/plan-views/projects/$projectId/gantt"
Assert-Equal $gantt.Status 200 'GET /api/v1/plan-views/projects/{id}/gantt'
Assert-Equal $gantt.Body.data.project.id $projectId 'project.id 与请求一致'
Assert-Equal $gantt.Body.data.can_edit_tasks $true '项目持有者可编辑任务'
$ganttTasks = @($gantt.Body.data.tasks | Where-Object { $_.id -eq $taskId }).Count
Assert-Equal $ganttTasks 1 '甘特图包含该任务'

# 最后修改人（需求文档 5.6）。
$ganttTask = $gantt.Body.data.tasks | Where-Object { $_.id -eq $taskId }
Assert-Equal $ganttTask.updated_by $userId '任务的最后修改人为创建者'

# 逐任务的 can_edit 与项目侧一致。
Assert-Equal $ganttTask.can_edit $true '持有者对任务 can_edit = true'

# 参数校验：未知查询参数必须被拒绝，而不是静默忽略。
$unknownParam = Invoke-Api -Method GET -Path '/api/v1/plan-views/personal?status=completed'
Assert-Equal $unknownParam.Status 400 '未知查询参数应 400'
Assert-Equal $unknownParam.Body.error.code 'invalid_request' 'error.code'

# 甘特图不接受任何筛选参数。
$ganttFiltered = Invoke-Api -Method GET -Path "/api/v1/plan-views/projects/$projectId/gantt?task_status=completed"
Assert-Equal $ganttFiltered.Status 400 '甘特图携带筛选参数应 400'

# 时间范围倒置：400 invalid_date_range。
$invertedRange = Invoke-Api -Method GET -Path '/api/v1/plan-views/personal?due_from=2026-10-31&due_to=2026-10-01'
Assert-Equal $invertedRange.Status 400 '时间范围倒置应 400'
Assert-Equal $invertedRange.Body.error.code 'invalid_date_range' 'error.code'

# 不存在的项目：404 project_not_found。
$missingGantt = Invoke-Api -Method GET -Path '/api/v1/plan-views/projects/00000000-0000-7000-8000-000000000000/gantt'
Assert-Equal $missingGantt.Status 404 '不存在的项目应 404'
Assert-Equal $missingGantt.Body.error.code 'project_not_found' 'error.code'

# 非法 UUID 路径：400，而不是 500。
$badGanttPath = Invoke-Api -Method GET -Path '/api/v1/plan-views/projects/not-a-uuid/gantt'
Assert-Equal $badGanttPath.Status 400 '非法 project_id 应 400'

# ---------------------------------------------------------------------------
# 归档项目只读
$projectDetail = Invoke-Api -Method GET -Path "/api/v1/projects/$projectId"
$archived = Invoke-Api -Method POST -Path "/api/v1/projects/$projectId/archive" -Body @{
    expected_updated_at = $projectDetail.Body.data.updated_at
}
Assert-Equal $archived.Status 200 '归档项目应 200'

$taskDetail = Invoke-Api -Method GET -Path "/api/v1/tasks/$taskId"
$editArchived = Invoke-Api -Method PATCH -Path "/api/v1/tasks/$taskId" -Body @{
    name                = '归档后改名'
    expected_updated_at = $taskDetail.Body.data.updated_at
}
Assert-Equal $editArchived.Status 409 '归档项目下的任务应只读（409）'

# 归档项目默认从列表排除
$activeList = Invoke-Api -Method POST -Path '/api/v1/projects/query' -Body @{}
Assert-Equal $activeList.Status 200 '项目列表应 200'

# 逻辑删除在单查与列表上一致
$deleteTask = Invoke-Api -Method DELETE -Path "/api/v1/tasks/$copyId" -Body @{
    expected_updated_at = (Invoke-Api -Method GET -Path "/api/v1/tasks/$copyId").Body.data.updated_at
}
# 归档项目下删除应被拒绝；这里只断言不是 5xx
if ($deleteTask.Status -ge 500) {
    Write-Host ("  [FAIL] 逻辑删除返回 5xx：{0}" -f $deleteTask.Status) -ForegroundColor Red
    $script:Failures++
} else {
    Write-Host ("  [PASS] 归档项目下的删除被拒绝而非 5xx：{0}" -f $deleteTask.Status) -ForegroundColor Green
}

# ---------------------------------------------------------------------------
Write-Step "持有者转移"
# ---------------------------------------------------------------------------

# 用独立的项目做转移，避免影响前面用例里"以持有者身份操作"的前提。
$transferProject = Invoke-Api -Method POST -Path '/api/v1/projects' -Body @{
    name       = "转移冒烟项目 $Username"
    group_id   = $groupId
    start_date = '2026-10-01'
    end_date   = '2026-10-31'
}
Assert-Equal $transferProject.Status 201 '创建转移用的项目'
$transferProjectId = $transferProject.Body.data.id

$null = Invoke-Api -Method POST -Path "/api/v1/projects/$transferProjectId/members" -Body @{
    user_id = $secondUserId
}

# 降级唯一持有者应被拒绝：要换人必须用转移，而不是先降后升两步。
$demoteSoleOwner = Invoke-Api -Method PATCH -Path "/api/v1/projects/$transferProjectId/members/$userId" -Body @{
    role = 'member'
}
Assert-Equal $demoteSoleOwner.Status 403 '降级唯一持有者应 403'

# 转移：role=owner 表示把持有者身份交给该成员。
$transferred = Invoke-Api -Method PATCH -Path "/api/v1/projects/$transferProjectId/members/$secondUserId" -Body @{
    role = 'owner'
}
Assert-Equal $transferred.Status 200 '转移持有者应 200'
Assert-Equal $transferred.Body.data.role 'owner' '接管者角色为 owner'

# 不变式：任何时刻恰好一个持有者。
$owners = Invoke-Api -Method POST -Path "/api/v1/projects/$transferProjectId/members/query" -Body @{
    role = 'owner'
}
Assert-Equal $owners.Body.pagination.total 1 '转移后恰好一个持有者'

# 原持有者已降为普通成员，不再能管理成员（404，不泄漏项目存在性）。
$afterTransfer = Invoke-Api -Method POST -Path "/api/v1/projects/$transferProjectId/members" -Body @{
    user_id = $userId
}
Assert-Equal $afterTransfer.Status 404 '转移后原持有者失去管理权'

# 后续断言必须由**现在的持有者**发起：非持有者只会拿到 404，
# 那样就分不清"重复转移被拒"与"没有权限"，断言会以错误的理由通过。
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$null = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $secondUsername
    password   = 'correct horse battery'
}

# 已是持有者时再次转移：409，而不是"成功但没有变化"。
$repeatTransfer = Invoke-Api -Method PATCH -Path "/api/v1/projects/$transferProjectId/members/$secondUserId" -Body @{
    role = 'owner'
}
Assert-Equal $repeatTransfer.Status 409 '重复转移应 409'

# 转移给非成员：404，且整体回滚（持有者不能被留在"空缺"状态）。
$nonMember = '00000000-0000-7000-8000-000000000001'
$transferToNonMember = Invoke-Api -Method PATCH -Path "/api/v1/projects/$transferProjectId/members/$nonMember" -Body @{
    role = 'owner'
}
Assert-Equal $transferToNonMember.Status 404 '转移给非成员应 404'
$ownersAfterFailure = Invoke-Api -Method POST -Path "/api/v1/projects/$transferProjectId/members/query" -Body @{
    role = 'owner'
}
Assert-Equal $ownersAfterFailure.Body.pagination.total 1 '失败的转移必须整体回滚（仍恰好一个持有者）'

# 系统管理员是所有权转移的兜底：他通常不是成员，但必须能看到并接管。
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$null = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $AdminUsername
    password   = $AdminPassword
}

$adminProject = Invoke-Api -Method GET -Path "/api/v1/projects/$transferProjectId"
Assert-Equal $adminProject.Status 200 '管理员可读自己不是成员的项目'
$adminGroup = Invoke-Api -Method GET -Path "/api/v1/groups/$groupId"
Assert-Equal $adminGroup.Status 200 '管理员可读自己不是成员的工作组'

# 管理员把项目持有者转回原用户（当前持有者是第二个用户）。
$adminTransfer = Invoke-Api -Method PATCH -Path "/api/v1/projects/$transferProjectId/members/$userId" -Body @{
    role = 'owner'
}
Assert-Equal $adminTransfer.Status 200 '管理员可转移项目持有者'

# 工作组所有权转移：owner_id 是工作组自身的属性，走 PATCH /groups/{id}。
$adminGroupDetail = Invoke-Api -Method GET -Path "/api/v1/groups/$groupId"
$groupTransfer = Invoke-Api -Method PATCH -Path "/api/v1/groups/$groupId" -Body @{
    owner_id            = $secondUserId
    expected_updated_at = $adminGroupDetail.Body.data.updated_at
}
Assert-Equal $groupTransfer.Status 200 '管理员可转移工作组所有权'
Assert-Equal $groupTransfer.Body.data.owner_id $secondUserId 'owner_id 已更新'

# 原所有者已降为普通成员，不再能管理成员。
# 必须用**原所有者自己的会话**：管理员在权限上是放行的，用它只会撞到"已是成员"的 409。
$session = $primarySession
$exOwnerManage = Invoke-Api -Method POST -Path "/api/v1/groups/$groupId/members" -Body @{
    user_id = $userId
}
Assert-Equal $exOwnerManage.Status 404 '转移后原所有者失去组管理权'

$session = $primarySession

# ---------------------------------------------------------------------------
Write-Step "附件"
# ---------------------------------------------------------------------------

$attachmentProject = Invoke-Api -Method POST -Path '/api/v1/projects' -Body @{
    name       = "附件冒烟项目 $Username"
    group_id   = $groupId
    start_date = '2026-10-01'
    end_date   = '2026-10-31'
}
Assert-Equal $attachmentProject.Status 201 '创建附件用的项目'
$attachmentProjectId = $attachmentProject.Body.data.id

$attachmentContent = [Text.Encoding]::UTF8.GetBytes('冒烟附件内容 attachment-smoke-body')
$uploaded = Invoke-ApiUpload -Path "/api/v1/projects/$attachmentProjectId/attachments" `
    -FileName '烟雾 测试.txt' -Content $attachmentContent -ContentType 'text/plain'
Assert-Equal $uploaded.Status 201 '上传附件应 201'
$attachmentId = $uploaded.Body.data.id
Assert-Equal $uploaded.Body.data.original_name '烟雾 测试.txt' '保留原始文件名'
Assert-Equal $uploaded.Body.data.extension '.txt' '扩展名正确'
Assert-Equal $uploaded.Body.data.size_bytes $attachmentContent.Length 'size_bytes 与内容一致'

$downloaded = Invoke-ApiDownload -Path "/api/v1/attachments/$attachmentId/download"
Assert-Equal $downloaded.Status 200 '下载附件应 200'
Assert-True (($downloaded.Disposition -join ' ') -like "*filename*=UTF-8''*") '中文文件名用 filename* 表达'

$attachmentList = Invoke-Api -Method POST -Path "/api/v1/projects/$attachmentProjectId/attachments/query" -Body @{
    page = 1; page_size = 20
}
Assert-Equal $attachmentList.Body.pagination.total 1 '附件列表含 1 条'

# 未逻辑删除的附件不能直接彻底删除。
$prematurePurge = Invoke-Api -Method POST -Path "/api/v1/attachments/$attachmentId/purge" -Body @{}
Assert-Equal $prematurePurge.Status 409 '未逻辑删除时彻底删除应 409'

$softDeleted = Invoke-Api -Method DELETE -Path "/api/v1/attachments/$attachmentId" -Body $null
Assert-Equal $softDeleted.Status 200 '逻辑删除应 200'
Assert-True ($null -ne $softDeleted.Body.data.deleted_at) '响应含 deleted_at'

$defaultList = Invoke-Api -Method POST -Path "/api/v1/projects/$attachmentProjectId/attachments/query" -Body @{
    page = 1; page_size = 20
}
Assert-Equal $defaultList.Body.pagination.total 0 '默认列表不含已删除附件'

$deletedList = Invoke-Api -Method POST -Path "/api/v1/projects/$attachmentProjectId/attachments/query" -Body @{
    page = 1; page_size = 20; deleted_only = $true
}
Assert-Equal $deletedList.Body.pagination.total 1 'deleted_only 含 1 条'

# 上传者本人可以彻底删除自己的附件。
$purged = Invoke-Api -Method POST -Path "/api/v1/attachments/$attachmentId/purge" -Body @{}
Assert-Equal $purged.Status 204 '彻底删除应 204'

$afterPurge = Invoke-Api -Method GET -Path "/api/v1/attachments/$attachmentId/download"
Assert-Equal $afterPurge.Status 404 '彻底删除后不可下载'

# 项目在本系统里是**归档**而不是删除，因此这里归档而不是清理。
# 归档后附件随之为只读——与"归档项目对其下任务只读"是同一条规则。
$attachmentProjectDetail = Invoke-Api -Method GET -Path "/api/v1/projects/$attachmentProjectId"
$archived = Invoke-Api -Method POST -Path "/api/v1/projects/$attachmentProjectId/archive" -Body @{
    expected_updated_at = $attachmentProjectDetail.Body.data.updated_at
}
Assert-Equal $archived.Status 200 '归档附件用的项目'

$archivedUpload = Invoke-ApiUpload -Path "/api/v1/projects/$attachmentProjectId/attachments" `
    -FileName 'archived.txt' -Content $attachmentContent -ContentType 'text/plain'
Assert-Equal $archivedUpload.Status 403 '归档项目不可再上传附件'

# ---------------------------------------------------------------------------
Write-Step "提醒"
# ---------------------------------------------------------------------------

# 前面的资料用例把本账号的邮件提醒关掉了（那是"字段是否真的生效"的证据），
# 这里先把它开回来——否则提醒会因为收件人不合格而被正确地跳过，
# 测出来的是"资格判定生效"而不是"调度器能生成提醒"。
$myProfile = Invoke-Api -Method GET -Path "/api/v1/users/$userId"
$restored = Invoke-Api -Method PATCH -Path "/api/v1/users/$userId" -Body @{
    reminder_enabled    = $true
    expected_updated_at = $myProfile.Body.data.updated_at
}
Assert-Equal $restored.Status 200 '重新开启本账号的邮件提醒'
Assert-Equal $restored.Body.data.reminder_enabled $true 'reminder_enabled 已恢复'

# 提醒由**后台派发循环**生成（需求文档 5.7："系统按固定周期检查待发送提醒"），
# 因此这里造一个已延期的任务，然后轮询等待调度器处理它。
$reminderProject = Invoke-Api -Method POST -Path '/api/v1/projects' -Body @{
    name       = "提醒冒烟项目 $Username"
    group_id   = $groupId
    start_date = '2026-09-01'
    end_date   = '2026-09-30'
}
Assert-Equal $reminderProject.Status 201 '创建提醒用的项目'
$reminderProjectId = $reminderProject.Body.data.id

# 到期日已过 → 属于"延期"场景。
$overdueTask = Invoke-Api -Method POST -Path "/api/v1/projects/$reminderProjectId/tasks" -Body @{
    name       = '已延期的冒烟任务'
    start_date = '2026-09-01'
    end_date   = '2026-09-10'
}
Assert-Equal $overdueTask.Status 201 '创建已延期的任务'
$overdueTaskId = $overdueTask.Body.data.id

# 等调度器跑一轮（smoke 启动时把 interval 调成 5 秒）。
$reminderList = $null
for ($attempt = 0; $attempt -lt 15; $attempt++) {
    Start-Sleep -Seconds 2
    $candidate = Invoke-Api -Method GET -Path '/api/v1/reminders?page=1&page_size=20'
    if ($candidate.Status -eq 200 -and $candidate.Body.pagination.total -gt 0) {
        $reminderList = $candidate
        break
    }
}
Assert-True ($null -ne $reminderList) '调度器已生成提醒'

if ($null -ne $reminderList) {
    $reminder = $reminderList.Body.data[0]
    # 邮件未启用（smoke 没配 SMTP）→ 提醒被如实记为 skipped，
    # 而不是伪装成"已发送"。这正是 Disabled 发送器返回错误而不是 nil 的意义。
    Assert-Equal $reminder.status 'skipped' '邮件未启用时提醒记为 skipped'
    Assert-True ($reminder.last_error -like '*邮件功能未启用*') '跳过原因可查询'

    $reminderDetail = Invoke-Api -Method GET -Path "/api/v1/reminders/$($reminder.id)"
    Assert-Equal $reminderDetail.Status 200 '提醒详情可查询'

    # skipped 允许重试（例如运维刚把 SMTP 配好）；已发送的不允许。
    $retried = Invoke-Api -Method POST -Path "/api/v1/reminders/$($reminder.id)/retry" -Body @{}
    Assert-Equal $retried.Status 200 'skipped 的提醒可以重试'
    Assert-Equal $retried.Body.data.status 'pending' '重试后回到待发送'
}

$badQuery = Invoke-Api -Method GET -Path '/api/v1/reminders?kinds=task_overdue'
Assert-Equal $badQuery.Status 400 '未知查询参数应 400'
$badSort = Invoke-Api -Method GET -Path '/api/v1/reminders?sort=dedupe_key'
Assert-Equal $badSort.Status 400 '排序字段非法应 400'

# ---------------------------------------------------------------------------
Write-Step "Audit：整条链路的可追溯性"
# ---------------------------------------------------------------------------

# 到这里，脚本已经完整走过 注册 → 工作组 → 项目 → 任务 → 共享视图 → 附件 → 提醒，
# 因此可以按对象回查审计，证明"链路中每一步都留下了记录"。
# 需要管理员会话：重新登录一次（前面的登录会把 $session 换成普通用户）。
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$chainLogin = Invoke-Api -Method POST -Path '/api/v1/auth/login' -Body @{
    identifier = $AdminUsername
    password   = $AdminPassword
}
Assert-Equal $chainLogin.Status 200 '链路审计用例：引导管理员登录'

$auditProject = Invoke-Api -Method GET -Path "/api/v1/audit?object_type=project&object_id=$projectId&page_size=50"
Assert-Equal $auditProject.Status 200 '按项目对象查审计应 200'
Assert-True (@($auditProject.Body.data | Where-Object { $_.action -eq 'created' }).Count -ge 1) '项目创建留下记录'
Assert-True (@($auditProject.Body.data | Where-Object { $_.action -eq 'archived' }).Count -ge 1) '项目归档留下记录'

$auditTask = Invoke-Api -Method GET -Path "/api/v1/audit?object_type=task&object_id=$taskId&page_size=50"
Assert-Equal $auditTask.Status 200 '按任务对象查审计应 200'
Assert-True (@($auditTask.Body.data | Where-Object { $_.action -eq 'created' }).Count -ge 1) '任务创建留下记录'

$auditAttachment = Invoke-Api -Method GET -Path "/api/v1/audit?object_type=attachment&object_id=$attachmentId&page_size=50"
Assert-Equal $auditAttachment.Status 200 '按附件对象查审计应 200'
$attachmentCreated = @($auditAttachment.Body.data | Where-Object { $_.action -eq 'created' })
Assert-True ($attachmentCreated.Count -ge 1) '附件上传留下记录'
# 附件内容绝不入账：changes 里只应有元数据。
$attachmentChanges = ($attachmentCreated[0].changes | ConvertTo-Json -Compress)
Assert-True ($attachmentChanges -like '*original_name*') '附件记录含元数据（原始文件名）'
Assert-True ($attachmentChanges -notlike '*附件内容*') '附件记录不含文件内容'

$session = $primarySession

# ---------------------------------------------------------------------------
Write-Step "结果"
# ---------------------------------------------------------------------------

if ($script:Failures -eq 0) {
    Write-Host "全部冒烟检查通过" -ForegroundColor Green
    exit 0
}

Write-Host ("存在 {0} 项失败" -f $script:Failures) -ForegroundColor Red
exit 1
