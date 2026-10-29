-- Stop dispatch and export delivery evidence before downgrading. Old dispatch
-- must remain disabled until its dedupe history has been reconciled.
DROP TRIGGER reminder_task_sync ON tasks;
DROP TRIGGER reminder_project_sync ON projects;
DROP TRIGGER reminder_task_member_sync ON task_members;
DROP TRIGGER reminder_project_member_sync ON project_members;
DROP TRIGGER reminder_user_sync ON users;
DROP TRIGGER reminder_group_sync ON groups;
DROP TRIGGER reminder_group_member_sync ON group_members;
DROP TRIGGER reminder_task_cycles ON tasks;
DROP FUNCTION reminder_business_changed();
DROP FUNCTION reminder_task_cycles();
DROP FUNCTION sync_project_reminders(uuid,timestamptz,boolean);
DROP FUNCTION sync_task_reminders(uuid,timestamptz,boolean);
DROP FUNCTION create_reminder_event(uuid,uuid,text,timestamptz);
DROP FUNCTION reminder_success(uuid,uuid,text,text,timestamptz);
DROP FUNCTION reminder_eligible(uuid,uuid);
DROP FUNCTION reminder_schedule(text,text,date,date,text,timestamptz,boolean);
DROP FUNCTION reminder_now();
DROP FUNCTION reminder_timezone(text);
DROP INDEX reminder_current_plan;
ALTER TABLE reminders DROP CONSTRAINT reminder_covered_attempt;
DROP TABLE reminder_delivery_guards;
DROP TABLE reminder_delivery_attempts;
ALTER TABLE reminders DROP CONSTRAINT ck_reminders_status;
UPDATE reminders SET status=CASE WHEN status IN ('sending','unknown') THEN 'failed' ELSE 'skipped' END
 WHERE status NOT IN ('pending','sent','failed','skipped');
ALTER TABLE reminders ADD CONSTRAINT ck_reminders_status CHECK(status IN ('pending','sent','failed','skipped'));
ALTER TABLE reminders DROP COLUMN cycle, DROP COLUMN event_id, DROP COLUMN target_date, DROP COLUMN plan_timezone,
 DROP COLUMN plan_version, DROP COLUMN covered_by, DROP COLUMN legacy;
ALTER TABLE tasks DROP COLUMN reminder_start_cycle, DROP COLUMN reminder_due_cycle;
