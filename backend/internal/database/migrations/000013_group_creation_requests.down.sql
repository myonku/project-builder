-- Destructive for application history: stop writes and export/backup before down.
-- Keep every approved group and its members. Only remove new workflow evidence.
DELETE FROM activity_logs WHERE object_type='group_creation_request' OR action='group_creation_reviewed';
DELETE FROM idempotency_keys WHERE scope='group_creation_requests.create';
DROP TABLE group_creation_requests;
ALTER TABLE activity_logs DROP CONSTRAINT ck_activity_logs_object_type;
ALTER TABLE activity_logs ADD CONSTRAINT ck_activity_logs_object_type CHECK (
    object_type IN ('user','group','project','task','attachment','reminder')
);
ALTER TABLE activity_logs ALTER COLUMN object_type TYPE VARCHAR(16);
ALTER TABLE activity_logs DROP CONSTRAINT ck_activity_logs_action;
ALTER TABLE activity_logs ADD CONSTRAINT ck_activity_logs_action CHECK (
    action IN ('created','updated','archived','deleted','purged','member_added','member_role_changed',
        'member_removed','owner_transferred','join_request_reviewed','shared','password_reset','disabled')
);
