-- 000002_tasks_updated_by.down.sql
-- 回退"最后修改人"列。
--
-- 约束必须先于列删除；索引随列一起被删除，无需单独处理。
-- 回退是有损的（最后修改人信息丢失），这是可接受的：该列的信息量等于
-- "当前指针"，完整历史属于 Audit（activity_logs）阶段。

ALTER TABLE tasks DROP CONSTRAINT fk_tasks_updated_by_users;
DROP INDEX IF EXISTS ix_tasks_updated_by;
ALTER TABLE tasks DROP COLUMN updated_by;
