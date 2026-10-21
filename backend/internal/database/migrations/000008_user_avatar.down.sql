-- 000008 回退：删除头像列与约束/索引。
DROP INDEX IF EXISTS uq_users_avatar_stored_key;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS ck_users_avatar_pair,
    DROP CONSTRAINT IF EXISTS ck_users_avatar_size_bytes,
    DROP COLUMN IF EXISTS avatar_updated_at,
    DROP COLUMN IF EXISTS avatar_size_bytes,
    DROP COLUMN IF EXISTS avatar_mime,
    DROP COLUMN IF EXISTS avatar_stored_key;
