-- 000006_registration_requests.down.sql
-- 回退注册申请表。这是有损回退：待审核/已拒绝的申请记录会丢失；
-- 已通过的申请已经转成 users 行，不受影响。
DROP TABLE registration_requests;
