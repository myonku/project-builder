-- P02: business facts and schedules commit together. The deferred triggers see
-- the final P00 rollup and final membership, including direct storage restores.
ALTER TABLE tasks ADD COLUMN reminder_start_cycle bigint NOT NULL DEFAULT 1;
ALTER TABLE tasks ADD COLUMN reminder_due_cycle bigint NOT NULL DEFAULT 1;
ALTER TABLE reminders ADD COLUMN cycle bigint NOT NULL DEFAULT 1;
ALTER TABLE reminders ADD COLUMN event_id uuid;
ALTER TABLE reminders ADD COLUMN target_date date;
ALTER TABLE reminders ADD COLUMN plan_timezone text NOT NULL DEFAULT 'UTC';
ALTER TABLE reminders ADD COLUMN plan_version bigint NOT NULL DEFAULT 1;
ALTER TABLE reminders ADD COLUMN covered_by uuid;
ALTER TABLE reminders ADD COLUMN legacy boolean NOT NULL DEFAULT true;
ALTER TABLE reminders ALTER COLUMN legacy SET DEFAULT false;
ALTER TABLE reminders DROP CONSTRAINT ck_reminders_status;
ALTER TABLE reminders ADD CONSTRAINT ck_reminders_status CHECK
 (status IN ('pending','sending','sent','failed','unknown','skipped','cancelled','superseded','suppressed'));

CREATE TABLE reminder_delivery_attempts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 reminder_id uuid NOT NULL REFERENCES reminders(id) ON DELETE CASCADE,
 task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 recipient_id uuid NOT NULL REFERENCES users(id), kind varchar(32) NOT NULL,
 plan_version bigint NOT NULL, cycle bigint NOT NULL, event_id uuid,
 authorized_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('sending','sent','failed','unknown')),
 sent_at timestamptz, timezone text NOT NULL, snapshot jsonb NOT NULL,
 error text NOT NULL DEFAULT '', resolution jsonb,
 CHECK(expires_at > authorized_at)
);
CREATE INDEX reminder_attempt_success ON reminder_delivery_attempts(task_id,recipient_id,kind,sent_at) WHERE status='sent';
CREATE TABLE reminder_delivery_guards (
 id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 recipient_id uuid NOT NULL REFERENCES users(id), kind varchar(32) NOT NULL,
 active_attempt uuid REFERENCES reminder_delivery_attempts(id),
 PRIMARY KEY(task_id,recipient_id,kind)
);
CREATE FUNCTION reminder_delivery_touched() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at:=clock_timestamp(); RETURN NEW; END $$;
CREATE TRIGGER reminder_attempt_touched BEFORE UPDATE ON reminder_delivery_attempts FOR EACH ROW EXECUTE FUNCTION reminder_delivery_touched();
CREATE TRIGGER reminder_guard_touched BEFORE UPDATE ON reminder_delivery_guards FOR EACH ROW EXECUTE FUNCTION reminder_delivery_touched();
ALTER TABLE reminders ADD CONSTRAINT reminder_covered_attempt FOREIGN KEY(covered_by) REFERENCES reminder_delivery_attempts(id);

CREATE FUNCTION reminder_now() RETURNS timestamptz LANGUAGE sql STABLE AS $$
 SELECT COALESCE(NULLIF(current_setting('worklane.reminder_now',true), '')::timestamptz, statement_timestamp())
$$;

-- Corrupt legacy timezone values use the same UTC fallback as read paths.
CREATE FUNCTION reminder_timezone(tz text) RETURNS text LANGUAGE plpgsql STABLE AS $$
BEGIN
 IF tz IS NULL OR btrim(tz)='' THEN RETURN 'UTC'; END IF;
 PERFORM timestamp '2000-01-01' AT TIME ZONE tz;
 RETURN tz;
EXCEPTION WHEN invalid_parameter_value THEN RETURN 'UTC';
END $$;

-- Pure rule function: no business reads and no implicit clock.
CREATE FUNCTION reminder_schedule(k text, st text, sd date, ed date, tz text, at_time timestamptz, immediate boolean)
RETURNS timestamptz LANGUAGE plpgsql STABLE AS $$
DECLARE today date := (at_time AT TIME ZONE reminder_timezone(tz))::date; d date; result timestamptz;
BEGIN
 tz:=reminder_timezone(tz);
 IF st IN ('completed','cancelled') THEN RETURN NULL; END IF;
 IF k='task_start_soon' THEN
   IF st<>'not_started' OR sd<=today THEN RETURN NULL; END IF; d:=sd-1;
 ELSIF k='task_due_soon' THEN
   IF ed<today THEN RETURN NULL; END IF; d:=ed-1;
 ELSIF k='task_overdue' THEN
   IF immediate AND ed<today THEN RETURN at_time; END IF;
   RETURN (ed+1+time '09:00') AT TIME ZONE tz;
 ELSE RETURN at_time;
 END IF;
 WHILE extract(isodow FROM d)>5 LOOP d:=d-1; END LOOP;
 result := (d+time '09:00') AT TIME ZONE tz;
 RETURN result;
END $$;

CREATE FUNCTION reminder_eligible(tid uuid, uid uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
 SELECT 1 FROM tasks t JOIN projects p ON p.id=t.project_id JOIN users u ON u.id=uid
 WHERE t.id=tid AND t.deleted_at IS NULL AND t.status NOT IN ('completed','cancelled')
 AND p.status<>'archived' AND p.archived_at IS NULL
 AND u.status='active' AND u.reminder_enabled AND btrim(u.email)<>''
 AND (EXISTS(SELECT 1 FROM task_members m WHERE m.task_id=t.id AND m.user_id=uid)
   OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=uid AND m.role='owner'))
 AND (u.system_role='admin' OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=uid)
   OR (t.is_shared AND p.visibility IN ('group_readonly','group_editable') AND EXISTS(
     SELECT 1 FROM groups g JOIN group_members m ON m.group_id=g.id
     WHERE g.id=p.group_id AND g.status='active' AND m.user_id=uid AND m.status='active')))
 )
$$;

CREATE FUNCTION reminder_success(tid uuid, uid uuid, k text, tz text, at_time timestamptz)
RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT id FROM reminder_delivery_attempts WHERE task_id=tid AND recipient_id=uid AND kind=k
 AND status='sent' AND (sent_at AT TIME ZONE tz)::date=(at_time AT TIME ZONE tz)::date
 ORDER BY sent_at DESC,id LIMIT 1
$$;

CREATE FUNCTION sync_task_reminders(tid uuid, at_time timestamptz, immediate boolean DEFAULT false)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE t tasks; u record; k text; c bigint; day date; due timestamptz; existing reminders; cover uuid; pid uuid;
BEGIN
 SELECT project_id INTO pid FROM tasks WHERE id=tid;
 IF pid IS NULL THEN RETURN; END IF;
 PERFORM 1 FROM projects WHERE id=pid FOR UPDATE;
 SELECT * INTO t FROM tasks WHERE id=tid FOR UPDATE;
 PERFORM 1 FROM reminder_delivery_guards WHERE task_id=tid ORDER BY recipient_id,kind FOR UPDATE;
 -- Membership loss cancels events as well as node plans. Unknown/sending are evidence, not cancellable work.
 UPDATE reminders r SET status=CASE WHEN NOT reminder_eligible(tid,r.recipient_id) THEN 'cancelled' ELSE 'superseded' END,
   last_error='资格失效或日期周期已替代', updated_at=at_time, plan_version=plan_version+1
 WHERE r.task_id=tid AND r.status IN ('pending','failed','skipped') AND
 (NOT reminder_eligible(tid,r.recipient_id)
 OR (r.kind='task_assigned' AND NOT EXISTS(SELECT 1 FROM task_members m WHERE m.task_id=tid AND m.user_id=r.recipient_id))
 OR (r.kind='task_owner_changed' AND NOT EXISTS(SELECT 1 FROM task_members m WHERE m.task_id=tid AND m.user_id=r.recipient_id AND m.role='owner'))
 OR (r.kind='task_start_soon' AND (r.cycle<>t.reminder_start_cycle OR t.status<>'not_started' OR t.start_date<=(at_time AT TIME ZONE r.plan_timezone)::date))
 OR (r.kind IN ('task_due_soon','task_overdue') AND r.cycle<>t.reminder_due_cycle)
 OR (r.kind='task_due_soon' AND t.end_date<(at_time AT TIME ZONE r.plan_timezone)::date));
 FOR u IN SELECT x.*,reminder_timezone(x.timezone) AS effective_timezone FROM users x WHERE reminder_eligible(tid,x.id) ORDER BY id LOOP
   FOR k IN SELECT unnest(ARRAY['task_due_soon','task_overdue','task_start_soon']) LOOP
     c:=CASE WHEN k='task_start_soon' THEN t.reminder_start_cycle ELSE t.reminder_due_cycle END;
     day:=CASE WHEN k='task_start_soon' THEN t.start_date ELSE t.end_date END;
     due:=reminder_schedule(k,t.status,t.start_date,t.end_date,u.effective_timezone,at_time,immediate);
     IF due IS NULL THEN CONTINUE; END IF;
     INSERT INTO reminder_delivery_guards(task_id,recipient_id,kind) VALUES(tid,u.id,k) ON CONFLICT DO NOTHING;
     SELECT * INTO existing FROM reminders WHERE task_id=tid AND recipient_id=u.id AND kind=k AND cycle=c AND NOT legacy
       ORDER BY created_at DESC,id DESC LIMIT 1;
     IF FOUND AND existing.status IN ('sent','suppressed','sending','unknown') THEN CONTINUE; END IF;
     cover:=NULL;
     IF due<=at_time THEN cover:=reminder_success(tid,u.id,k,u.effective_timezone,at_time); END IF;
     IF existing.id IS NOT NULL THEN
       -- Ordinary edits cannot advance natural overdue 09:00 or retry failures.
       IF existing.status IN ('pending','failed') AND existing.plan_timezone=u.effective_timezone THEN due:=existing.scheduled_for; END IF;
       UPDATE reminders SET scheduled_for=due, recipient_email=u.email, plan_timezone=u.effective_timezone,
         status=CASE WHEN cover IS NOT NULL THEN 'suppressed' WHEN existing.status='failed' THEN 'failed' ELSE 'pending' END,
         covered_by=cover, last_error=CASE WHEN cover IS NOT NULL THEN '同日已发送' WHEN existing.status='failed' THEN last_error ELSE '' END,
         updated_at=at_time, plan_version=plan_version+1 WHERE id=existing.id;
     ELSE
       INSERT INTO reminders(id,task_id,recipient_id,recipient_email,kind,scheduled_for,dedupe_key,cycle,target_date,plan_timezone,status,covered_by,last_error)
       VALUES(gen_random_uuid(),tid,u.id,u.email,k,due,concat(k,':',tid,':',u.id,':cycle:',c),c,day,u.effective_timezone,
         CASE WHEN cover IS NULL THEN 'pending' ELSE 'suppressed' END,cover,CASE WHEN cover IS NULL THEN '' ELSE '同日已发送' END)
       ON CONFLICT(dedupe_key) DO NOTHING;
     END IF;
   END LOOP;
 END LOOP;
END $$;

CREATE FUNCTION sync_project_reminders(pid uuid, at_time timestamptz, immediate boolean DEFAULT false)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE tid uuid;
BEGIN
 PERFORM 1 FROM projects WHERE id=pid FOR UPDATE;
 FOR tid IN SELECT id FROM tasks WHERE project_id=pid ORDER BY id LOOP
   PERFORM sync_task_reminders(tid,at_time,immediate);
 END LOOP;
END $$;

CREATE FUNCTION reminder_task_cycles() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.start_date IS DISTINCT FROM OLD.start_date THEN NEW.reminder_start_cycle:=OLD.reminder_start_cycle+1; END IF;
 IF NEW.end_date IS DISTINCT FROM OLD.end_date THEN NEW.reminder_due_cycle:=OLD.reminder_due_cycle+1; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reminder_task_cycles BEFORE UPDATE ON tasks FOR EACH ROW EXECUTE FUNCTION reminder_task_cycles();

CREATE FUNCTION reminder_business_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pid uuid; tid uuid; at_time timestamptz:=reminder_now(); immediate boolean:=false; uid uuid;
BEGIN
 IF TG_TABLE_NAME='tasks' THEN
   IF TG_OP='UPDATE' AND (OLD.start_date,OLD.end_date,OLD.status,OLD.deleted_at,OLD.is_shared) IS NOT DISTINCT FROM (NEW.start_date,NEW.end_date,NEW.status,NEW.deleted_at,NEW.is_shared) THEN RETURN NULL; END IF;
   IF TG_OP='INSERT' THEN immediate:=true;
   ELSE immediate:=NEW.end_date IS DISTINCT FROM OLD.end_date OR (OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL)
     OR (OLD.status IN ('completed','cancelled') AND NEW.status NOT IN ('completed','cancelled')); END IF;
   PERFORM sync_task_reminders(NEW.id,at_time,immediate);
 ELSIF TG_TABLE_NAME='projects' THEN
   IF TG_OP='UPDATE' AND (OLD.status='archived',OLD.archived_at,OLD.visibility) IS NOT DISTINCT FROM (NEW.status='archived',NEW.archived_at,NEW.visibility) THEN RETURN NULL; END IF;
   IF TG_OP='UPDATE' THEN immediate:=OLD.status='archived' AND NEW.status<>'archived'; END IF;
   PERFORM sync_project_reminders(NEW.id,at_time,immediate);
 ELSIF TG_TABLE_NAME='task_members' THEN
   tid:=CASE WHEN TG_OP='DELETE' THEN OLD.task_id ELSE NEW.task_id END;
   PERFORM sync_task_reminders(tid,at_time,TG_OP='INSERT');
   -- Events refer to a real transition and must still describe the final relationship.
   IF TG_OP<>'DELETE' AND EXISTS(SELECT 1 FROM task_members WHERE id=NEW.id) THEN
     IF TG_OP='INSERT' THEN PERFORM create_reminder_event(tid,NEW.user_id,'task_assigned',at_time);
     ELSIF OLD.role<>'owner' AND NEW.role='owner' AND EXISTS(SELECT 1 FROM task_members WHERE id=NEW.id AND role='owner') THEN
       PERFORM create_reminder_event(tid,NEW.user_id,'task_owner_changed',at_time);
     END IF;
   END IF;
 ELSIF TG_TABLE_NAME='project_members' THEN
   pid:=CASE WHEN TG_OP='DELETE' THEN OLD.project_id ELSE NEW.project_id END;
   PERFORM sync_project_reminders(pid,at_time,TG_OP='INSERT');
 ELSIF TG_TABLE_NAME='users' THEN
   immediate:=(OLD.status<>'active' AND NEW.status='active') OR (NOT OLD.reminder_enabled AND NEW.reminder_enabled);
   FOR pid IN SELECT DISTINCT t.project_id FROM tasks t WHERE EXISTS(SELECT 1 FROM task_members m WHERE m.task_id=t.id AND m.user_id=NEW.id)
       OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=t.project_id AND m.user_id=NEW.id) ORDER BY t.project_id LOOP
     PERFORM sync_project_reminders(pid,at_time,immediate);
   END LOOP;
 ELSE
   IF TG_TABLE_NAME='groups' THEN uid:=NEW.id;
   ELSIF TG_OP='DELETE' THEN uid:=OLD.group_id; ELSE uid:=NEW.group_id; END IF;
   FOR pid IN SELECT id FROM projects WHERE group_id=uid ORDER BY id LOOP
     PERFORM sync_project_reminders(pid,at_time,false);
   END LOOP;
 END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION create_reminder_event(tid uuid, uid uuid, k text, at_time timestamptz)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE cover uuid; eid uuid:=gen_random_uuid(); tz text; addr text;
BEGIN
 IF NOT reminder_eligible(tid,uid) THEN RETURN; END IF;
 SELECT reminder_timezone(timezone),email INTO tz,addr FROM users WHERE id=uid;
 INSERT INTO reminder_delivery_guards(task_id,recipient_id,kind) VALUES(tid,uid,k) ON CONFLICT DO NOTHING;
 PERFORM 1 FROM reminder_delivery_guards WHERE task_id=tid AND recipient_id=uid AND kind=k FOR UPDATE;
 UPDATE reminders SET status='superseded',last_error='新事件已替代',updated_at=at_time
   WHERE task_id=tid AND recipient_id=uid AND kind=k AND status IN ('pending','failed','skipped');
 cover:=reminder_success(tid,uid,k,tz,at_time);
 INSERT INTO reminders(id,task_id,recipient_id,recipient_email,kind,scheduled_for,dedupe_key,event_id,plan_timezone,status,covered_by,last_error)
 VALUES(eid,tid,uid,addr,k,at_time,'event:'||eid,eid,tz,CASE WHEN cover IS NULL THEN 'pending' ELSE 'suppressed' END,cover,
 CASE WHEN cover IS NULL THEN '' ELSE '同日已发送' END);
END $$;

CREATE CONSTRAINT TRIGGER reminder_task_sync AFTER INSERT OR UPDATE ON tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_project_sync AFTER INSERT OR UPDATE ON projects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_task_member_sync AFTER INSERT OR UPDATE OR DELETE ON task_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_project_member_sync AFTER INSERT OR UPDATE OR DELETE ON project_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_user_sync AFTER UPDATE ON users DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN ((OLD.email,OLD.timezone,OLD.status,OLD.reminder_enabled,OLD.system_role) IS DISTINCT FROM (NEW.email,NEW.timezone,NEW.status,NEW.reminder_enabled,NEW.system_role)) EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_group_sync AFTER UPDATE ON groups DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();
CREATE CONSTRAINT TRIGGER reminder_group_member_sync AFTER INSERT OR UPDATE OR DELETE ON group_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reminder_business_changed();

-- Preserve old evidence. Pending is recalculated only by the explicit initializer;
-- old failures lack protocol evidence and cannot become safely retryable.
UPDATE reminders r SET plan_timezone=reminder_timezone(u.timezone),target_date=CASE WHEN r.kind='task_start_soon' THEN t.start_date ELSE t.end_date END
 FROM users u,tasks t WHERE u.id=r.recipient_id AND t.id=r.task_id;
INSERT INTO reminder_delivery_attempts(id,reminder_id,task_id,recipient_id,kind,plan_version,cycle,authorized_at,expires_at,status,sent_at,timezone,snapshot,error)
 SELECT gen_random_uuid(),id,task_id,recipient_id,kind,plan_version,cycle,COALESCE(sent_at,created_at),COALESCE(sent_at,created_at)+interval '5 minutes',
 CASE WHEN status='sent' THEN 'sent' ELSE 'unknown' END,sent_at,plan_timezone,
 jsonb_build_object('legacy',true,'email',recipient_email,'original_status',status,'original_error',last_error,'timezone_source','current_user_projection'),last_error
 FROM reminders WHERE status IN ('sent','failed');
INSERT INTO reminder_delivery_guards(task_id,recipient_id,kind,active_attempt)
 SELECT DISTINCT ON (task_id,recipient_id,kind) task_id,recipient_id,kind,id FROM reminder_delivery_attempts WHERE status='unknown'
 ORDER BY task_id,recipient_id,kind,authorized_at,id;
UPDATE reminders SET status='unknown' WHERE status='failed';
UPDATE reminders SET status='superseded',last_error='升级后由初始化命令重算' WHERE status='pending';
-- Conservatively associate old successes with current node cycles, retaining actual sent_at.
UPDATE reminders SET legacy=false,dedupe_key=concat(kind,':',task_id,':',recipient_id,':cycle:',cycle)
 WHERE kind IN ('task_start_soon','task_due_soon','task_overdue') AND status='sent'
 AND id IN (SELECT DISTINCT ON(task_id,recipient_id,kind) id FROM reminders WHERE status='sent' ORDER BY task_id,recipient_id,kind,sent_at DESC,id);
CREATE UNIQUE INDEX reminder_current_plan ON reminders(task_id,recipient_id,kind) WHERE status='pending';
