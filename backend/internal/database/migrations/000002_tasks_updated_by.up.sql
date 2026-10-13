-- 000002_tasks_updated_by.up.sql
-- 为 tasks 增加"最后修改人"。
--
-- 背景（docs/00-需求文档.md 5.6）：
--   组内汇总页要求"查看任务最后修改人和最后修改时间"。
--   时间已有（tasks.updated_at），但**没有任何列能回答最后修改人是谁**——
--   created_by 是创建者，updated_at 只是时间。
--   完整的操作历史属于 Audit（activity_logs）阶段；本列是"当前指针"，
--   让列表与甘特图可以在**一次查询**里给出最后修改人，而无需为每一行回查历史表。
--
-- 语义（必须在每条写语句中与 updated_at 一起维护）：
--   updated_by 恒等于"最近一次修改 tasks 行的用户"。
--   它跟踪的事件与 updated_at 完全一致：成员变更、附件变更不触碰 tasks 行，
--   因此既不改 updated_at 也不改 updated_by。
--   这样"两个字段一起推进"就成为一条可以用测试锁住的不变式。
--
-- 类型选择：
--   NOT NULL：任务总是有人创建的，且创建者一定存在，因此没有"未知"状态。
--   现有行按 created_by 回填（生产环境尚未部署，但测试库有数据，必须能原地升级）。
--   ON DELETE RESTRICT：与 created_by 一致；本系统的用户一律停用而非物理删除。

ALTER TABLE tasks ADD COLUMN updated_by UUID;

-- 回填：把"谁创建的"作为已知的最佳近似，保证 NOT NULL 可以加上。
UPDATE tasks SET updated_by = created_by WHERE updated_by IS NULL;

ALTER TABLE tasks ALTER COLUMN updated_by SET NOT NULL;

ALTER TABLE tasks
    ADD CONSTRAINT fk_tasks_updated_by_users
    FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT;

-- 支撑组内汇总按"最近修改"排序与排查。
CREATE INDEX ix_tasks_updated_by ON tasks (updated_by);
