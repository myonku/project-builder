-- 001_init.up.sql
-- 项目/计划管理系统初始 schema。
--
-- 本迁移把早期实现迁移链中的 6 个 revision 合并为一组干净的初始迁移
-- （见 docs/02-数据库模型与迁移.md 4）：
--   0001_persistence_base（空基线）与 0004_archive_timestamp（类型修正）只是历史演进产物，
--   新项目从零开始直接按最终形态建表更清晰，且生产环境尚未部署，不存在迁移兼容负担。
--
-- 约定：
--   * 主键 UUID，由应用层用 UUIDv7 生成（时间有序）。
--   * 所有业务表包含 id / created_at / updated_at。
--   * 瞬时时间列统一 TIMESTAMPTZ，存储 UTC。
--   * 业务日期列使用 DATE（不含时分秒）。
--   * 枚举字段一律带 CHECK 约束。
--   * 外键删除行为必须显式声明，不得依赖默认值。
--   * updated_at 由应用层在每条写语句中显式 SET updated_at = now()，
--     本 schema 不引入 ON UPDATE 触发器（见 docs/03-配置与运行.md 6）。

-- ---------------------------------------------------------------------------
-- users
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id               UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    username         VARCHAR(64) NOT NULL,
    email            VARCHAR(320) NOT NULL,
    display_name     VARCHAR(128) NOT NULL,
    password_hash    VARCHAR(512) NOT NULL,
    status           VARCHAR(16) NOT NULL DEFAULT 'active',
    system_role      VARCHAR(16) NOT NULL DEFAULT 'member',
    timezone         VARCHAR(64) NOT NULL DEFAULT 'UTC',
    reminder_enabled BOOLEAN     NOT NULL DEFAULT true,
    last_login_at    TIMESTAMPTZ NULL,
    CONSTRAINT pk_users PRIMARY KEY (id),
    CONSTRAINT ck_users_user_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_users_user_system_role CHECK (system_role IN ('member', 'admin')),
    CONSTRAINT uq_users_username UNIQUE (username),
    CONSTRAINT uq_users_email UNIQUE (email)
);

-- 用户名与邮箱按不区分大小写唯一。
-- 应用层在写入前统一转小写；这两个索引是"并发注册同一账号"的最终保证，
-- 使重复注册必然触发唯一约束冲突而不是产生两个账号。
CREATE UNIQUE INDEX uq_users_username_lower ON users (lower(username));
CREATE UNIQUE INDEX uq_users_email_lower ON users (lower(email));

-- ---------------------------------------------------------------------------
-- groups
-- ---------------------------------------------------------------------------
CREATE TABLE groups (
    id          UUID         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    name        VARCHAR(160) NOT NULL,
    description VARCHAR(2000) NULL,
    status      VARCHAR(16)  NOT NULL DEFAULT 'active',
    owner_id    UUID         NOT NULL,
    CONSTRAINT pk_groups PRIMARY KEY (id),
    CONSTRAINT ck_groups_group_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT fk_groups_owner_id_users
        FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- 已知差异（docs/02-数据库模型与迁移.md 3.2）：
-- 工作组所有者唯一性没有数据库约束，owner_id 只是普通外键，目前仅由应用层保证。
-- 该差异在此显式声明，避免后续误以为存在数据库保证。

-- ---------------------------------------------------------------------------
-- group_members
-- ---------------------------------------------------------------------------
CREATE TABLE group_members (
    id         UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    group_id   UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    role       VARCHAR(16) NOT NULL DEFAULT 'member',
    status     VARCHAR(16) NOT NULL DEFAULT 'active',
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_group_members PRIMARY KEY (id),
    CONSTRAINT ck_group_members_group_member_role CHECK (role IN ('member', 'manager')),
    CONSTRAINT ck_group_members_group_member_status CHECK (status IN ('active', 'removed')),
    CONSTRAINT fk_group_members_group_id_groups
        FOREIGN KEY (group_id) REFERENCES groups (id) ON DELETE CASCADE,
    CONSTRAINT fk_group_members_user_id_users
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT group_member_identity UNIQUE (group_id, user_id)
);

CREATE INDEX ix_group_members_user_id ON group_members (user_id);

-- ---------------------------------------------------------------------------
-- group_join_requests
-- ---------------------------------------------------------------------------
CREATE TABLE group_join_requests (
    id          UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    group_id    UUID        NOT NULL,
    user_id     UUID        NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'pending',
    reviewed_by UUID        NULL,
    reviewed_at TIMESTAMPTZ NULL,
    CONSTRAINT pk_group_join_requests PRIMARY KEY (id),
    CONSTRAINT ck_group_join_requests_group_join_request_status
        CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT fk_group_join_requests_group_id_groups
        FOREIGN KEY (group_id) REFERENCES groups (id) ON DELETE CASCADE,
    CONSTRAINT fk_group_join_requests_user_id_users
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_group_join_requests_reviewed_by_users
        FOREIGN KEY (reviewed_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT group_join_request_identity UNIQUE (group_id, user_id)
);

-- ---------------------------------------------------------------------------
-- projects
-- ---------------------------------------------------------------------------
CREATE TABLE projects (
    id          UUID         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    name        VARCHAR(200) NOT NULL,
    description VARCHAR(4000) NULL,
    created_by  UUID         NOT NULL,
    group_id    UUID         NOT NULL,
    status      VARCHAR(20)  NOT NULL DEFAULT 'not_started',
    visibility  VARCHAR(20)  NOT NULL DEFAULT 'private',
    start_date  DATE         NOT NULL,
    end_date    DATE         NOT NULL,
    color       VARCHAR(32)  NULL,
    -- 必须是带时区类型。早期实现曾把它建成不带时区，靠后续迁移 0004_archive_timestamp 修正。
    archived_at TIMESTAMPTZ  NULL,
    CONSTRAINT pk_projects PRIMARY KEY (id),
    CONSTRAINT ck_projects_project_status CHECK (
        status IN ('not_started', 'in_progress', 'paused', 'completed', 'archived', 'cancelled')
    ),
    CONSTRAINT ck_projects_project_visibility CHECK (
        visibility IN ('private', 'group_readonly', 'group_editable')
    ),
    CONSTRAINT ck_projects_project_date_range CHECK (end_date >= start_date),
    CONSTRAINT fk_projects_created_by_users
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    -- 项目必须且只能归属一个工作组（需求文档 4.1）。
    CONSTRAINT fk_projects_group_id_groups
        FOREIGN KEY (group_id) REFERENCES groups (id) ON DELETE RESTRICT
);

CREATE INDEX ix_projects_group_id ON projects (group_id);
CREATE INDEX ix_projects_created_by ON projects (created_by);
CREATE INDEX ix_projects_status ON projects (status);
-- 列表默认按结束日期排序，并以 id 作为次级稳定键（接口文档 2.5）。
CREATE INDEX ix_projects_end_date_id ON projects (end_date, id);

-- ---------------------------------------------------------------------------
-- project_members
-- ---------------------------------------------------------------------------
CREATE TABLE project_members (
    id         UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    project_id UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    role       VARCHAR(16) NOT NULL DEFAULT 'member',
    CONSTRAINT pk_project_members PRIMARY KEY (id),
    CONSTRAINT ck_project_members_project_member_role CHECK (role IN ('owner', 'member')),
    CONSTRAINT fk_project_members_project_id_projects
        FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    -- 成员可以来自项目归属组以外的其他工作组，因此不做组归属复合外键。
    CONSTRAINT fk_project_members_user_id_users
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT project_member_identity UNIQUE (project_id, user_id)
);

-- 核心不变式：一个项目最多一个 owner。由数据库保证，应用层不得只做提示。
CREATE UNIQUE INDEX project_single_owner
    ON project_members (project_id) WHERE role = 'owner';

CREATE INDEX ix_project_members_user_id ON project_members (user_id);

-- ---------------------------------------------------------------------------
-- tasks
-- ---------------------------------------------------------------------------
CREATE TABLE tasks (
    id           UUID         NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    project_id   UUID         NOT NULL,
    parent_id    UUID         NULL,
    created_by   UUID         NOT NULL,
    name         VARCHAR(200) NOT NULL,
    description  VARCHAR(4000) NULL,
    -- 注意：没有 overdue 状态。逾期是派生展示字段，不是持久化状态。
    status       VARCHAR(20)  NOT NULL DEFAULT 'not_started',
    progress     INTEGER      NOT NULL DEFAULT 0,
    priority     INTEGER      NOT NULL DEFAULT 0,
    start_date   DATE         NOT NULL,
    end_date     DATE         NOT NULL,
    is_milestone BOOLEAN      NOT NULL DEFAULT false,
    is_shared    BOOLEAN      NOT NULL DEFAULT false,
    deleted_at   TIMESTAMPTZ  NULL,
    CONSTRAINT pk_tasks PRIMARY KEY (id),
    CONSTRAINT ck_tasks_task_status CHECK (
        status IN ('not_started', 'in_progress', 'completed', 'cancelled')
    ),
    CONSTRAINT ck_tasks_task_progress CHECK (progress BETWEEN 0 AND 100),
    CONSTRAINT ck_tasks_task_date_range CHECK (end_date >= start_date),
    CONSTRAINT fk_tasks_project_id_projects
        FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    -- 自引用：父任务被物理删除时子任务级联删除（业务上以逻辑删除为主）。
    CONSTRAINT fk_tasks_parent_id_tasks
        FOREIGN KEY (parent_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_tasks_created_by_users
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE INDEX ix_tasks_project_id ON tasks (project_id);
CREATE INDEX ix_tasks_parent_id ON tasks (parent_id);
-- 默认排除逻辑删除；部分索引让"未删除任务"的列表与单查共用同一条件。
CREATE INDEX ix_tasks_project_id_deleted_at ON tasks (project_id, deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX ix_tasks_end_date_id ON tasks (end_date, id);

-- ---------------------------------------------------------------------------
-- task_members
-- ---------------------------------------------------------------------------
CREATE TABLE task_members (
    id         UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    task_id    UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    role       VARCHAR(16) NOT NULL DEFAULT 'member',
    CONSTRAINT pk_task_members PRIMARY KEY (id),
    CONSTRAINT ck_task_members_task_member_role CHECK (role IN ('owner', 'member')),
    CONSTRAINT fk_task_members_task_id_tasks
        FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_task_members_user_id_users
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT task_member_identity UNIQUE (task_id, user_id)
);

-- 核心不变式：一个任务最多一个 owner。
CREATE UNIQUE INDEX task_single_owner
    ON task_members (task_id) WHERE role = 'owner';

CREATE INDEX ix_task_members_user_id ON task_members (user_id);

-- ---------------------------------------------------------------------------
-- idempotency_keys
-- ---------------------------------------------------------------------------
-- 历史缺陷对照：早期实现完全未实现 Idempotency-Key，同一 key 连续两次
-- POST /tasks/{id}/copy 会产生两个任务。本表提供持久化 + 唯一约束作为最终保证。
CREATE TABLE idempotency_keys (
    id                  UUID         NOT NULL,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    user_id             UUID         NOT NULL,
    -- 作用域（如 tasks.copy）。必须包含操作语义，避免跨操作串用同一 key。
    scope               VARCHAR(120) NOT NULL,
    idempotency_key     VARCHAR(200) NOT NULL,
    result_id           UUID         NOT NULL,
    result_secondary_id UUID         NULL,
    CONSTRAINT pk_idempotency_keys PRIMARY KEY (id),
    CONSTRAINT fk_idempotency_keys_user_id_users
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    -- 幂等键作用域包含用户与操作，避免跨用户串用（架构设计 5.6）。
    CONSTRAINT idempotency_key_identity UNIQUE (user_id, scope, idempotency_key)
);

CREATE INDEX idempotency_key_scope ON idempotency_keys (user_id, scope);
