-- 000003_attachments.up.sql
-- 项目/任务附件（需求文档 3.1「项目和任务附件」）。
--
-- 设计要点（docs/02-数据库模型与迁移.md 5.2 的规划要点逐条落实）：
--
--   * 归属**项目或任务，二者其一**：用 CHECK 约束保证 project_id 与 task_id
--     恰有一个非空。允许两列同时为空或同时非空都会让"这个附件属于谁"变成
--     运行期才知道的事，而权限判定完全依赖它。
--
--   * stored_key 是**存储标识**，绝不由用户提供的文件名派生。
--     原名只作为展示字段保存（original_name），因此恶意文件名（"../x"、
--     超长、同名冲突）无法影响磁盘布局。
--     stored_key 上加唯一约束：两行指向同一个对象是不可恢复的状态
--     （任何一方被彻底删除都会让另一方读到空洞）。
--
--   * 逻辑删除用 deleted_at（与其它业务表一致）：删除后仍占磁盘，
--     可被"彻底删除"回收。不引入回收站与过期自动清理（用户已确认暂不需要）。
--
--   * 外键删除行为显式声明：附件随项目/任务物理删除而级联，
--     上传者一律 RESTRICT（本系统的用户只停用、不物理删除）。
--     注意：项目/任务本身是**逻辑删除**（tasks.deleted_at）或归档，
--     因此正常运行中不会触发级联，它只是数据完整性兜底。
--
--   * 单用户最多 1000 份（storage.max_files_per_user）与扩展名白名单
--     是**应用层**规则，不进 schema：它们的取值可配置，
--     写进 CHECK 会让"调整配置"变成"改 schema"。

CREATE TABLE attachments (
    id            UUID         NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    project_id    UUID         NULL,
    task_id       UUID         NULL,
    uploader_id   UUID         NOT NULL,
    -- 展示用原始文件名；长度上限与 VARCHAR 一致，超长由应用层截断/拒绝。
    original_name VARCHAR(255) NOT NULL,
    -- 存储标识（相对路径）。禁止由用户输入派生。
    stored_key    VARCHAR(512) NOT NULL,
    -- 扩展名含点号的小写形式（如 ".pdf"）；白名单按它匹配。
    extension     VARCHAR(32)  NOT NULL,
    size_bytes    BIGINT       NOT NULL,
    -- 上传时记录的 MIME；无法判定时为空串（下载时回退为 octet-stream）。
    mime_type     VARCHAR(160) NOT NULL DEFAULT '',
    deleted_at    TIMESTAMPTZ  NULL,
    CONSTRAINT pk_attachments PRIMARY KEY (id),
    CONSTRAINT ck_attachments_single_container CHECK (
        (project_id IS NOT NULL AND task_id IS NULL)
        OR (project_id IS NULL AND task_id IS NOT NULL)
    ),
    CONSTRAINT ck_attachments_size CHECK (size_bytes >= 0),
    CONSTRAINT uq_attachments_stored_key UNIQUE (stored_key),
    CONSTRAINT fk_attachments_project_id_projects
        FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT fk_attachments_task_id_tasks
        FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_attachments_uploader_id_users
        FOREIGN KEY (uploader_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- 列表查询按归属过滤，且默认只看未删除的，因此用部分索引。
CREATE INDEX ix_attachments_project_id ON attachments (project_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_attachments_task_id ON attachments (task_id) WHERE deleted_at IS NULL;

-- 配额检查按上传者统计（含已逻辑删除的：它们仍占磁盘，见 docs/04 4.8）。
CREATE INDEX ix_attachments_uploader_id ON attachments (uploader_id);
