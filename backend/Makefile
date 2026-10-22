# 项目/计划管理系统 Go 服务端 —— 常用命令入口
#
# 约定：所有可重复的本地操作都通过本文件暴露，避免在文档里散落各版本命令。
# 环境变量优先级与运行配置一致（见 backend/docs/B03-配置与运行.md）。

SHELL := pwsh
.DEFAULT_GOAL := help

# 集成测试依赖（PostgreSQL / Redis / MongoDB / Mailpit，见 docker/compose.test.yml）。
TEST_COMPOSE := docker/compose.test.yml
TEST_ENV     := docker/test.env
TEST_DATABASE_URL := postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable
TEST_REDIS_ADDR   := 127.0.0.1:16379
TEST_REDIS_PASSWORD := project_builder_test_redis_password
TEST_MONGO_URI := mongodb://127.0.0.1:27018
TEST_MONGO_DATABASE := project_builder_test

# 测试环境的引导管理员（仅测试凭据，与 docker/config.test.toml 保持一致）。
# 冒烟脚本用这组凭据覆盖管理员端点的成功路径。
TEST_BOOTSTRAP_ADMIN_USERNAME := bootstrap_admin
TEST_BOOTSTRAP_ADMIN_EMAIL    := bootstrap_admin@test.local
TEST_BOOTSTRAP_ADMIN_PASSWORD := bootstrap_admin_test_password

BINARY_DIR := bin
API_BINARY := $(BINARY_DIR)/api.exe
MIGRATE_BINARY := $(BINARY_DIR)/migrate.exe

.PHONY: help
help: ## 显示可用命令
	@Write-Host "可用命令：" -ForegroundColor Cyan
	@Select-String -Path $(firstword $(MAKEFILE_LIST)) -Pattern '^([a-zA-Z_-]+):.*?## (.*)$$' | ForEach-Object { $m = $_.Matches[0]; Write-Host ("  {0,-22} {1}" -f $m.Groups[1].Value, $m.Groups[2].Value) }

# ---------------------------------------------------------------------------
# 构建与静态检查
# ---------------------------------------------------------------------------

.PHONY: build
build: ## 编译 api 与 migrate 到 bin/
	@New-Item -ItemType Directory -Force -Path $(BINARY_DIR) | Out-Null
	go build -o $(API_BINARY) ./cmd/api
	go build -o $(MIGRATE_BINARY) ./cmd/migrate
	@Write-Host "构建完成：$(API_BINARY) / $(MIGRATE_BINARY)" -ForegroundColor Green

.PHONY: fmt
fmt: ## 格式化全部 Go 代码
	gofmt -w ./cmd ./internal

.PHONY: fmt-check
fmt-check: ## 校验格式（不修改文件），有差异即失败
	@$$diff = gofmt -l ./cmd ./internal; if ($$diff) { Write-Host "以下文件未格式化：" -ForegroundColor Red; $$diff; exit 1 } else { Write-Host "格式检查通过" -ForegroundColor Green }

.PHONY: vet
vet: ## 运行 go vet
	go vet ./...

.PHONY: tidy
tidy: ## 整理依赖
	go mod tidy

.PHONY: check
check: fmt-check vet ## 本地提交前的静态检查

# ---------------------------------------------------------------------------
# 测试
# ---------------------------------------------------------------------------

.PHONY: test
test: ## 运行全部测试（单元 + 集成）
	# 集成测试共用同一个物理数据库，跨包并行会互相清空数据。
	# 测试包内部已用 PostgreSQL 咨询锁串行化；这里再显式串行编译，
	# 使无依赖机器上也保持一致行为。
	go test ./... -count=1 -p 1

.PHONY: test-unit
test-unit: ## 只运行不需要外部依赖的单元测试
	go test $$(go list ./... | Where-Object { $$_ -notmatch 'testsupport' }) -count=1 -short

# 注意：下面两条 recipe 必须是**单个物理行**。本 Makefile 没有 .ONESHELL，
# GNU make 会为每条 recipe 行单独启动一个 shell，因此用行尾反引号拼出来的多行
# `$env:` 赋值不会存活到 `go run` / `go test` 那一行（实测：行尾反引号让
# `pwsh -Command` 以 1 退出）。需要新增变量时继续接在同一行里。
.PHONY: test-integration
test-integration: ## 只运行需要真实 PostgreSQL / Redis / MongoDB 的集成测试
	$$env:TEST_DATABASE_DSN = "$(TEST_DATABASE_URL)"; $$env:TEST_REDIS_ADDR = "$(TEST_REDIS_ADDR)"; $$env:TEST_REDIS_PASSWORD = "$(TEST_REDIS_PASSWORD)"; $$env:TEST_MONGO_URI = "$(TEST_MONGO_URI)"; $$env:TEST_MONGO_DATABASE = "$(TEST_MONGO_DATABASE)"; go test ./internal/testsupport/... ./internal/app/auth/... ./internal/platform/storage/... -count=1 -v -p 1

.PHONY: test-race
test-race: ## 以竞态检测器运行测试
	go test ./... -count=1 -race -p 1

.PHONY: cover
cover: ## 生成覆盖率报告
	go test ./... -count=1 -p 1 -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | Select-Object -Last 1

# ---------------------------------------------------------------------------
# 测试依赖（PostgreSQL 16 / Redis 7 / MongoDB / Mailpit）
# ---------------------------------------------------------------------------

.PHONY: deps-up
deps-up: ## 启动测试依赖容器
	docker compose --env-file $(TEST_ENV) -f $(TEST_COMPOSE) up -d
	docker compose --env-file $(TEST_ENV) -f $(TEST_COMPOSE) ps

.PHONY: deps-down
deps-down: ## 停止测试依赖容器（保留数据卷）
	docker compose --env-file $(TEST_ENV) -f $(TEST_COMPOSE) down

.PHONY: deps-reset
deps-reset: ## 停止测试依赖并删除数据卷（下次启动为全新数据库）
	docker compose --env-file $(TEST_ENV) -f $(TEST_COMPOSE) down -v

.PHONY: deps-logs
deps-logs: ## 查看测试依赖日志
	docker compose --env-file $(TEST_ENV) -f $(TEST_COMPOSE) logs -f

# ---------------------------------------------------------------------------
# 迁移（默认指向本地开发库；用 DATABASE_URL 覆盖）
# ---------------------------------------------------------------------------

.PHONY: migrate-up
migrate-up: ## 应用全部迁移
	go run ./cmd/migrate up

.PHONY: migrate-down
migrate-down: ## 回退全部迁移（清空 schema）
	go run ./cmd/migrate down

.PHONY: migrate-version
migrate-version: ## 显示当前迁移版本与脏标记
	go run ./cmd/migrate version

.PHONY: migrate-test-up
migrate-test-up: ## 对测试库应用全部迁移
	$$env:DATABASE_URL = "$(TEST_DATABASE_URL)"; go run ./cmd/migrate up

.PHONY: migrate-test-down
migrate-test-down: ## 对测试库回退全部迁移
	$$env:DATABASE_URL = "$(TEST_DATABASE_URL)"; go run ./cmd/migrate down

# ---------------------------------------------------------------------------
# 运行
# ---------------------------------------------------------------------------

.PHONY: run
run: ## 以本地开发配置启动 HTTP 服务
	go run ./cmd/api

# SCHEDULER_INTERVAL_SECONDS=5 是 `make smoke` 能通过的前提：冒烟对
# "调度器已生成提醒"只轮询 30 秒，而 config.toml 的 interval 是 60 秒
# （见 backend/docs/B05-进度与验证记录.md 7.0.1 第 4 条坑）。
.PHONY: run-test-config
run-test-config: ## 以测试依赖启动 HTTP 服务（PostgreSQL 15432 / Redis 16379 / Mongo 27018，监听 127.0.0.1:8000）
	$$env:DATABASE_URL = "$(TEST_DATABASE_URL)"; $$env:REDIS_HOST = "127.0.0.1"; $$env:REDIS_PORT = "16379"; $$env:REDIS_PASSWORD = "$(TEST_REDIS_PASSWORD)"; $$env:MONGO_URI = "$(TEST_MONGO_URI)"; $$env:MONGO_DATABASE = "$(TEST_MONGO_DATABASE)"; $$env:SECURITY_SESSION_SECRET = "local-dev-session-secret-change-me"; $$env:SECURITY_BOOTSTRAP_ADMIN_USERNAME = "$(TEST_BOOTSTRAP_ADMIN_USERNAME)"; $$env:SECURITY_BOOTSTRAP_ADMIN_EMAIL = "$(TEST_BOOTSTRAP_ADMIN_EMAIL)"; $$env:SECURITY_BOOTSTRAP_ADMIN_PASSWORD = "$(TEST_BOOTSTRAP_ADMIN_PASSWORD)"; $$env:SECURITY_REGISTRATION_REQUIRES_APPROVAL = "false"; $$env:SCHEDULER_INTERVAL_SECONDS = "5"; go run ./cmd/api

.PHONY: smoke
smoke: ## 对已启动的服务做一次冒烟检查（默认 http://127.0.0.1:8000，即 run-test-config 的地址）
	pwsh -File ./scripts/smoke.ps1 -BaseUrl http://127.0.0.1:8000

.PHONY: clean
clean: ## 删除构建产物
	Remove-Item -Recurse -Force $(BINARY_DIR) -ErrorAction SilentlyContinue
	Remove-Item -Force coverage.out,coverage.html -ErrorAction SilentlyContinue
