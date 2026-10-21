-- 000008: 用户头像（P01 §4.2）。
--
-- 头像字节走与附件相同的 storage.Store；本表只保存定位与响应所需的元数据：
--   * avatar_stored_key 永不返回客户端；
--   * avatar_updated_at 是唯一的对外版本字段，用于前端拼 ?v= 破缓存与 ETag；
--   * 头像更新不推进 users.updated_at，避免与资料表单的版本协议冲突。
ALTER TABLE users
    ADD COLUMN avatar_stored_key VARCHAR(512),
    ADD COLUMN avatar_mime VARCHAR(160) NOT NULL DEFAULT '',
    ADD COLUMN avatar_size_bytes BIGINT NOT NULL DEFAULT 0
        CONSTRAINT ck_users_avatar_size_bytes CHECK (avatar_size_bytes >= 0),
    ADD COLUMN avatar_updated_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_users_avatar_pair
        CHECK ((avatar_stored_key IS NULL) = (avatar_updated_at IS NULL));

-- 一个存储对象只允许被一个用户引用；NULL 不参与唯一性（部分唯一索引）。
CREATE UNIQUE INDEX uq_users_avatar_stored_key
    ON users (avatar_stored_key)
    WHERE avatar_stored_key IS NOT NULL;
