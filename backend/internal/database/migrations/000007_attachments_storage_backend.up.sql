-- 000007: 记录每个附件的字节实际落在哪个对象存储后端。
--
-- 该列只是迁移账本，不参与读路由：服务始终使用当前配置的 storage.Store。
-- 旧版本迁移上来时默认 local；切到 gridfs 后由 storage-migrate 逐行更新。
ALTER TABLE attachments
    ADD COLUMN storage_backend VARCHAR(16) NOT NULL DEFAULT 'local'
    CONSTRAINT ck_attachments_storage_backend CHECK (storage_backend IN ('local', 'gridfs'));

CREATE INDEX ix_attachments_storage_backend ON attachments (storage_backend);
