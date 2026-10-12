-- 000005_activity_logs.up.sql
-- 操作记录（Audit）表：记录"谁、什么时候、对什么对象、做了什么、改成了什么"。
--
-- 需求文档 5.8：记录创建、修改、归档或删除项目与任务、修改持有者、日期、状态、
-- 进度、共享设置、成员关系及邮件发送结果等关键操作；至少保存操作人、时间、
-- 对象、动作和关键变更内容。
--
-- 三个刻意的设计选择（改动前先看 docs/02-数据库模型与迁移.md 3.2.7）：
--   1. **一行即一条记录，append-only**：审计没有"更新"语义，
--      因此没有 deleted_at，也不提供写以外的端点。
--      与业务写入同事务（docs/04 §4.10），业务回滚则这行也不存在。
--   2. **changes 用 JSONB 且只存"新值"**：需求只要求"关键变更内容"，
--      不要求完整前后对照；完整前后值会把密码/令牌/邮件正文一类内容带进这张表。
--   3. **object_id 不建外键**：成员变更记在**容器对象**上（docs/05 §6 第 38 条），
--      而对象本身可能被彻底删除（附件）或逻辑删除；若加外键，
--      审计反而会阻止合法删除，或者随着级联删除一起消失——两者都与"审计"的目相悖。
--      actor_id 保留外键（用户一律停用、不物理删除，见 docs/02 §3.1）。
--
-- 枚举取值必须与 domain 常量、docs/04 §4.10 三处一致，有测试断言
-- （TestAuditEnumWhitelistMatchesDatabaseCheck）。

CREATE TABLE activity_logs (
    id          UUID         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- 追加写的表没有更新的语义；这一列只为满足"所有业务表都有审计列"的
    -- 全局约定（testsupport 的 TestEveryBusinessTableHasAuditColumns 会检查）。
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- 操作人。用户在本系统里一律停用而不物理删除，因此 RESTRICT 不会成为障碍。
    actor_id    UUID         NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    action      VARCHAR(32)  NOT NULL,
    object_type VARCHAR(16)  NOT NULL,
    -- 被操作对象的 id（成员变更记容器 id，见上面的说明）。
    object_id   UUID         NOT NULL,
    -- "字段名 → 新值"的扁平对象；键与值都由领域层做白名单与截断，数据库不再约束内容。
    changes     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    -- 便于把一次请求产生的多条审计串起来排查；没有 requestID 时为空串。
    request_id  VARCHAR(64)  NOT NULL DEFAULT '',

    CONSTRAINT pk_activity_logs PRIMARY KEY (id),
    CONSTRAINT ck_activity_logs_action CHECK (
        action IN (
            'created', 'updated', 'archived', 'deleted', 'purged',
            'member_added', 'member_role_changed', 'member_removed',
            'owner_transferred', 'join_request_reviewed', 'shared',
            'password_reset', 'disabled'
        )
    ),
    CONSTRAINT ck_activity_logs_object_type CHECK (
        object_type IN ('user', 'group', 'project', 'task', 'attachment', 'reminder')
    )
);

-- 主查询："这个对象上发生过什么"（按时间倒序）。
CREATE INDEX ix_activity_logs_object
    ON activity_logs (object_type, object_id, created_at DESC);

-- "这个人做过什么"。
CREATE INDEX ix_activity_logs_actor
    ON activity_logs (actor_id, created_at DESC);

-- "最近有哪些删除/转移"这类按动作排查。
CREATE INDEX ix_activity_logs_action
    ON activity_logs (action, created_at DESC);

-- 纯时间范围查询（不带任何对象筛选时的默认排序）。
CREATE INDEX ix_activity_logs_created_at
    ON activity_logs (created_at DESC);
