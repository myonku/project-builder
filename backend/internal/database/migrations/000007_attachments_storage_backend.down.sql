-- 000007 down：删掉迁移账本列。
--
-- 有损：回退后无法知道某行字节在 local 还是 gridfs；因此回退前必须先把
-- 所有对象搬回 local，并确认没有 storage_backend='gridfs' 的行。
DROP INDEX IF EXISTS ix_attachments_storage_backend;
ALTER TABLE attachments DROP CONSTRAINT IF EXISTS ck_attachments_storage_backend;
ALTER TABLE attachments DROP COLUMN IF EXISTS storage_backend;
