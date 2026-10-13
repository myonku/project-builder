-- 000004_reminders.up.sql
-- 邮件提醒（需求文档 5.7）。
--
-- 设计要点：
--
--   * **这一行是"提醒记录"而不是"发送队列"**。需求文档 5.7 要求
--     "每条提醒记录任务、收件人、提醒类型、计划时间、发送结果、失败原因和重试次数"，
--     因此发送结果、失败原因、重试次数都是这条记录的一部分，不另建队列表。
--
--   * dedupe_key 上加唯一约束，是"同一任务、同一收件人、同一提醒类型和同一时间窗口
--     不得重复发送"的**唯一可靠实现**。生成侧用 INSERT ... ON CONFLICT DO NOTHING，
--     于是"周期性扫描"可以放心地反复运行而不产生重复邮件。
--     key 里必须包含**时间窗口**（如到期日），否则"到期前提醒"一辈子只会发一次。
--
--   * recipient_email 是**发送时刻的收件地址快照**。用户之后改了邮箱，
--     历史记录仍应显示当时发到了哪里——这正是"邮件失败可查询原因"的前提。
--
--   * scheduled_for 是**计划发送时刻**（由用例层按工作日规则算出），
--     与 created_at 不同：一条提醒可以在计划时刻之前就生成好。
--
--   * 外键删除行为显式声明：随任务物理删除而级联；收件人一律 RESTRICT
--     （用户只停用、不物理删除）。

CREATE TABLE reminders (
    id              UUID         NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    task_id         UUID         NOT NULL,
    recipient_id    UUID         NOT NULL,
    -- 收件地址快照（发送时使用）。
    recipient_email VARCHAR(320) NOT NULL,
    -- task_assigned / task_start_soon / task_due_soon / task_overdue / task_owner_changed
    kind            VARCHAR(32)  NOT NULL,
    -- 计划发送时刻。
    scheduled_for   TIMESTAMPTZ  NOT NULL,
    -- pending / sent / failed / skipped
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    -- 幂等键：同一任务 + 收件人 + 类型 + 时间窗口只允许一条。
    dedupe_key      VARCHAR(255) NOT NULL,
    attempts        INTEGER      NOT NULL DEFAULT 0,
    last_error      TEXT         NOT NULL DEFAULT '',
    sent_at         TIMESTAMPTZ  NULL,
    CONSTRAINT pk_reminders PRIMARY KEY (id),
    CONSTRAINT uq_reminders_dedupe_key UNIQUE (dedupe_key),
    CONSTRAINT ck_reminders_status CHECK (
        status IN ('pending', 'sent', 'failed', 'skipped')
    ),
    CONSTRAINT ck_reminders_attempts CHECK (attempts >= 0),
    CONSTRAINT fk_reminders_task_id_tasks
        FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_reminders_recipient_id_users
        FOREIGN KEY (recipient_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- 派发扫描：找"计划时刻已到且仍待发送"的记录，按计划时刻先后处理。
CREATE INDEX ix_reminders_dispatch ON reminders (scheduled_for) WHERE status = 'pending';

-- 用户的提醒查看入口：按收件人 + 时间倒序。
CREATE INDEX ix_reminders_recipient ON reminders (recipient_id, created_at DESC);

-- 任务维度的排查（"这个任务的提醒都发给谁了"）。
CREATE INDEX ix_reminders_task ON reminders (task_id);
