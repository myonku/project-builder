-- 000005_activity_logs.down.sql
-- 回退操作记录表。
--
-- 这是一次**有损**回退，而且是本项目里损失最明确的一次：
-- 审计记录是**唯一**的操作历史，没有任何业务表能重新推导出它
-- （tasks.updated_by 只是"最后修改人"这一个指针，不是历史）。
-- 因此回退前应当先导出：SELECT * FROM activity_logs（或用 pg_dump 只导出这一张表）。
--
-- 索引与约束随表一起删除，无需单独处理。
DROP TABLE activity_logs;
