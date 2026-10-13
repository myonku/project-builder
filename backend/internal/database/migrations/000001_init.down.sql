-- 001_init.down.sql
-- 回退初始 schema。
--
-- 要求（docs/02-数据库模型与迁移.md 5）：up 与 down 必须成对且可逆。
-- 验收标准：down 回退后可再次成功执行 up（见 internal/database/migrate_test.go）。

-- 按依赖倒序删除；索引随表一并删除，无需单独 DROP INDEX。
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS task_members;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS group_join_requests;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS users;
