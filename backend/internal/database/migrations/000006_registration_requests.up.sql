-- 000006_registration_requests.up.sql
-- 自助注册申请（管理员审核后才会创建 users 行）。
--
-- 设计要点（backend/docs/B02-数据库模型与迁移.md §3.1）：
--   1. 申请与账号分离：pending 申请不是用户，不能登录、不会出现在用户列表；
--      只有 review 通过时，才在同一事务里写入 users 并把申请标记为 approved。
--   2. password_hash 只在申请表中保存摘要，绝不保存明文；审核通过时直接复用该摘要，
--      不要求用户重新输入密码。
--   3. 只对 pending 申请做 (lower(username), lower(email)) 唯一约束：
--      被拒绝后允许重新申请，而并发提交两个相同 pending 申请会被数据库挡下。
--   4. reviewed_by 外键指向 users：用户一律停用而不物理删除，因此 RESTRICT 不会造成障碍。
--   5. updated_at 由应用层显式 SET（与 000001 的约定一致），不引入触发器。

CREATE TABLE registration_requests (
    id               UUID         NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    username         VARCHAR(64)  NOT NULL,
    email            VARCHAR(320) NOT NULL,
    display_name     VARCHAR(128) NOT NULL,
    password_hash    VARCHAR(512) NOT NULL,
    timezone         VARCHAR(64)  NOT NULL DEFAULT 'UTC',
    status           VARCHAR(16)  NOT NULL DEFAULT 'pending',
    reviewed_by      UUID         NULL REFERENCES users (id) ON DELETE RESTRICT,
    reviewed_at      TIMESTAMPTZ  NULL,
    rejection_reason VARCHAR(2000) NULL,

    CONSTRAINT pk_registration_requests PRIMARY KEY (id),
    CONSTRAINT ck_registration_requests_status
        CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT ck_registration_requests_review
        CHECK (
            (status = 'pending'
                AND reviewed_by IS NULL AND reviewed_at IS NULL AND rejection_reason IS NULL)
            OR (status = 'approved'
                AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND rejection_reason IS NULL)
            OR (status = 'rejected'
                AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL)
        )
);

-- 同一时刻只允许一个待审核申请占用该用户名/邮箱；并发重复提交由数据库兜底。
CREATE UNIQUE INDEX uq_registration_requests_username_lower
    ON registration_requests (lower(username))
    WHERE status = 'pending';
CREATE UNIQUE INDEX uq_registration_requests_email_lower
    ON registration_requests (lower(email))
    WHERE status = 'pending';

-- 审核页默认按状态 + 提交时间倒序。
CREATE INDEX ix_registration_requests_status_created
    ON registration_requests (status, created_at DESC);
