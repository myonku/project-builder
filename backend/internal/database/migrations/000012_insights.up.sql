-- One baseline and an atomic, replayable final task state per business transaction.
-- DDL locks serialize baseline creation with all affected writers.
LOCK TABLE projects, tasks, task_members IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE insights_coverage (id boolean PRIMARY KEY DEFAULT true CHECK(id), started_at timestamptz NOT NULL);
INSERT INTO insights_coverage VALUES (true, clock_timestamp());
CREATE TABLE insight_states (
 task_id uuid PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 snapshot jsonb NOT NULL
);
CREATE TABLE insight_events (
 id bigserial PRIMARY KEY, task_id uuid NOT NULL,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 transaction_id bigint NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 actor_id uuid, before_state jsonb, after_state jsonb NOT NULL,
 baseline boolean NOT NULL DEFAULT false,
 UNIQUE(task_id,transaction_id)
);
CREATE INDEX insight_events_task_time ON insight_events(task_id,occurred_at,id);
CREATE INDEX insight_events_time ON insight_events(occurred_at,id);

CREATE FUNCTION insight_snapshot(target uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('id',t.id,'project_id',t.project_id,'group_id',p.group_id,
 'name',t.name,'project_name',p.name,'status',t.status,'progress',t.progress,
 'start_date',t.start_date,'end_date',t.end_date,'has_children',t.has_children,
 'deleted',t.deleted_at IS NOT NULL,'is_shared',t.is_shared,'visibility',p.visibility,
 'archived',p.archived_at IS NOT NULL OR p.status='archived','is_milestone',t.is_milestone,
 'priority',t.priority,'members',COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'user_id',m.user_id,'display_name',u.display_name,'username',u.username,'role',m.role) ORDER BY m.user_id)
 FROM task_members m JOIN users u ON u.id=m.user_id WHERE m.task_id=t.id),'[]'::jsonb))
 FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=target
$$;
INSERT INTO insight_states SELECT id,insight_snapshot(id) FROM tasks;
INSERT INTO insight_events(task_id,project_id,transaction_id,occurred_at,after_state,baseline)
 SELECT t.id,t.project_id,txid_current(),c.started_at,s.snapshot,true
 FROM tasks t JOIN insight_states s ON s.task_id=t.id CROSS JOIN insights_coverage c;

CREATE FUNCTION capture_insight(target uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE previous jsonb; current_value jsonb; actor uuid;
BEGIN
 current_value := insight_snapshot(target);
 IF current_value IS NULL THEN RETURN; END IF;
 SELECT snapshot INTO previous FROM insight_states WHERE task_id=target FOR UPDATE;
 IF previous IS NOT DISTINCT FROM current_value THEN RETURN; END IF;
 SELECT actor_id INTO actor FROM activity_logs WHERE xmin::text::bigint=txid_current()
 AND object_id IN (target,(current_value->>'project_id')::uuid) ORDER BY created_at DESC,id DESC LIMIT 1;
 INSERT INTO insight_events(task_id,project_id,transaction_id,actor_id,before_state,after_state)
 VALUES(target,(current_value->>'project_id')::uuid,txid_current(),actor,previous,current_value)
 ON CONFLICT(task_id,transaction_id) DO UPDATE SET after_state=excluded.after_state;
 INSERT INTO insight_states VALUES(target,current_value) ON CONFLICT(task_id) DO UPDATE SET snapshot=excluded.snapshot;
END $$;
CREATE FUNCTION insight_task_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='task_members' THEN
   IF TG_OP<>'DELETE' THEN PERFORM capture_insight(NEW.task_id); END IF;
   IF TG_OP<>'INSERT' THEN PERFORM capture_insight(OLD.task_id); END IF;
 ELSE
   IF TG_OP<>'DELETE' THEN PERFORM capture_insight(NEW.id); END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER insight_task_changed AFTER INSERT OR UPDATE ON tasks
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION insight_task_changed();
CREATE CONSTRAINT TRIGGER insight_members_changed AFTER INSERT OR UPDATE OR DELETE ON task_members
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION insight_task_changed();
CREATE FUNCTION insight_project_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE task record;
BEGIN
 IF (OLD.group_id,OLD.visibility,OLD.archived_at,OLD.name) IS NOT DISTINCT FROM
 (NEW.group_id,NEW.visibility,NEW.archived_at,NEW.name) THEN RETURN NULL; END IF;
 FOR task IN SELECT id FROM tasks WHERE project_id=NEW.id ORDER BY id LOOP PERFORM capture_insight(task.id); END LOOP;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER insight_project_changed AFTER UPDATE ON projects
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION insight_project_changed();
