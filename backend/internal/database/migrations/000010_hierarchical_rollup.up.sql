-- Effective values are materialized in the same transaction as task mutations.
-- Application writers lock the project before locking any task in that project.
ALTER TABLE tasks ADD COLUMN has_children boolean NOT NULL DEFAULT false;
ALTER TABLE tasks ADD COLUMN precise_progress numeric NOT NULL DEFAULT 0;
UPDATE tasks SET precise_progress = progress;
ALTER TABLE projects ADD COLUMN has_tasks boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100);
ALTER TABLE projects ADD COLUMN precise_progress numeric NOT NULL DEFAULT 0;

-- Preserve original manual values for an explicit, lossless migration rollback.
CREATE TABLE rollup_legacy_values AS
SELECT 'task'::text AS kind, id, status, progress FROM tasks
UNION ALL SELECT 'project', id, status, 0 FROM projects;

CREATE FUNCTION refresh_project_rollup(target uuid, actor uuid DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $$
DECLARE node record; n integer; live integer; value numeric; result text;
BEGIN
  PERFORM 1 FROM projects WHERE id = target FOR UPDATE;
  -- Leaves retain their last effective value on losing their final child.
  UPDATE tasks t SET has_children = false, precise_progress = progress,
    updated_by = CASE WHEN has_children THEN COALESCE(actor, updated_by) ELSE updated_by END,
    updated_at = CASE WHEN has_children THEN clock_timestamp() ELSE updated_at END
  WHERE project_id = target AND NOT EXISTS
    (SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL);

  FOR node IN
    WITH RECURSIVE tree AS (
      SELECT id, 1 AS depth FROM tasks WHERE project_id = target AND parent_id IS NULL
      UNION ALL SELECT c.id, tree.depth + 1 FROM tasks c JOIN tree ON c.parent_id = tree.id
      WHERE tree.depth < 32
    ) SELECT t.id FROM tree JOIN tasks t USING (id)
      WHERE EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL)
      ORDER BY tree.depth DESC
  LOOP
    SELECT count(*), count(*) FILTER (WHERE status <> 'cancelled'),
      avg(precise_progress) FILTER (WHERE status <> 'cancelled'),
      CASE WHEN bool_and(status = 'cancelled') THEN 'cancelled'
        WHEN bool_and(status = 'completed' AND precise_progress = 100) FILTER (WHERE status <> 'cancelled') THEN 'completed'
        WHEN bool_or(status <> 'not_started' OR precise_progress > 0) FILTER (WHERE status <> 'cancelled') THEN 'in_progress'
        ELSE 'not_started' END
      INTO n, live, value, result FROM tasks WHERE parent_id = node.id AND deleted_at IS NULL;
    value := COALESCE(value, 0);
    UPDATE tasks SET status = result, progress = round(value), precise_progress = value,
      has_children = true, updated_by = COALESCE(actor, updated_by), updated_at = clock_timestamp()
    WHERE id = node.id AND (status, progress, precise_progress, has_children)
      IS DISTINCT FROM (result, round(value)::integer, value, true);
  END LOOP;

  SELECT count(*) INTO n FROM tasks WHERE project_id = target AND deleted_at IS NULL;
  IF n > 0 THEN
    -- Children of a deleted parent contribute as roots until that parent is restored.
    SELECT COALESCE(avg(t.precise_progress) FILTER (WHERE t.status <> 'cancelled'), 0),
      CASE WHEN bool_and(t.status = 'cancelled') THEN 'cancelled'
        WHEN bool_and(t.status = 'completed' AND t.precise_progress = 100) FILTER (WHERE t.status <> 'cancelled') THEN 'completed'
        WHEN bool_or(t.status <> 'not_started' OR t.precise_progress > 0) FILTER (WHERE t.status <> 'cancelled') THEN 'in_progress'
        ELSE 'not_started' END INTO value, result
    FROM tasks t WHERE t.project_id = target AND t.deleted_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM tasks p WHERE p.id = t.parent_id AND p.deleted_at IS NULL);
    UPDATE projects SET progress = round(value), precise_progress = value, has_tasks = true,
      status = CASE WHEN archived_at IS NOT NULL OR status = 'archived' THEN 'archived' ELSE result END,
      updated_at = clock_timestamp() WHERE id = target;
  ELSE
    UPDATE projects SET has_tasks = false, updated_at = clock_timestamp()
      WHERE id = target AND has_tasks;
  END IF;
END $$;

CREATE FUNCTION task_rollup_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF pg_trigger_depth() > 1 THEN RETURN NULL; END IF;
  IF TG_OP <> 'DELETE' THEN PERFORM refresh_project_rollup(NEW.project_id, NEW.updated_by); END IF;
  IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.project_id <> NEW.project_id) THEN
    PERFORM refresh_project_rollup(OLD.project_id);
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER task_rollup_changed AFTER INSERT OR UPDATE OR DELETE ON tasks
FOR EACH ROW EXECUTE FUNCTION task_rollup_changed();

-- Run after the application has acquired the project lock, so concurrent tree
-- mutations cannot pass validation against different snapshots.
CREATE FUNCTION validate_rollup_tree() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent tasks; depth integer; height integer; cycle boolean;
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.parent_id, NEW.start_date, NEW.end_date)
    IS NOT DISTINCT FROM (OLD.parent_id, OLD.start_date, OLD.end_date) THEN RETURN NEW; END IF;
  IF NEW.parent_id IS NOT NULL THEN
    SELECT * INTO parent FROM tasks WHERE id = NEW.parent_id;
    IF NOT FOUND OR parent.deleted_at IS NOT NULL OR parent.project_id <> NEW.project_id THEN
      RAISE EXCEPTION 'invalid parent' USING ERRCODE = '23514', CONSTRAINT = 'ck_rollup_parent';
    END IF;
    WITH RECURSIVE ancestors AS (
      SELECT id, parent_id, 1 AS d FROM tasks WHERE id = NEW.parent_id
      UNION ALL SELECT t.id, t.parent_id, a.d+1 FROM tasks t JOIN ancestors a ON a.parent_id=t.id WHERE a.d<32
    ) SELECT max(d), bool_or(id = NEW.id) INTO depth, cycle FROM ancestors;
    IF cycle OR NEW.start_date < parent.start_date OR NEW.end_date > parent.end_date THEN
      RAISE EXCEPTION 'cycle or parent date range' USING ERRCODE = '23514', CONSTRAINT = 'ck_rollup_parent';
    END IF;
  ELSE depth := 0;
  END IF;
  WITH RECURSIVE descendants AS (
    SELECT id, 1 AS d FROM tasks WHERE parent_id = NEW.id
    UNION ALL SELECT t.id, d.d+1 FROM tasks t JOIN descendants d ON t.parent_id=d.id WHERE d.d<32
  ) SELECT COALESCE(max(d),0) INTO height FROM descendants;
  IF depth + 1 + height > 3 THEN
    RAISE EXCEPTION 'maximum depth is three' USING ERRCODE = '23514', CONSTRAINT = 'ck_rollup_parent';
  END IF;
  IF EXISTS (SELECT 1 FROM tasks WHERE parent_id=NEW.id AND deleted_at IS NULL
    AND (start_date < NEW.start_date OR end_date > NEW.end_date)) THEN
    RAISE EXCEPTION 'child date range' USING ERRCODE = '23514', CONSTRAINT = 'ck_rollup_parent';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER validate_rollup_tree BEFORE INSERT OR UPDATE ON tasks
FOR EACH ROW EXECUTE FUNCTION validate_rollup_tree();

DO $$ DECLARE p record; BEGIN
  FOR p IN SELECT id FROM projects ORDER BY id LOOP PERFORM refresh_project_rollup(p.id); END LOOP;
END $$;
