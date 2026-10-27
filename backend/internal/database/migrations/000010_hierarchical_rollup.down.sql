DROP TRIGGER task_rollup_changed ON tasks;
DROP FUNCTION task_rollup_changed();
DROP TRIGGER IF EXISTS validate_rollup_tree ON tasks;
DROP FUNCTION IF EXISTS validate_rollup_tree();
DROP FUNCTION IF EXISTS refresh_project_rollup(uuid, uuid);
DROP FUNCTION IF EXISTS refresh_project_rollup(uuid);
UPDATE tasks t SET status = l.status, progress = l.progress
FROM rollup_legacy_values l WHERE l.kind = 'task' AND l.id = t.id;
UPDATE projects p SET status = l.status
FROM rollup_legacy_values l WHERE l.kind = 'project' AND l.id = p.id;
DROP TABLE rollup_legacy_values;
ALTER TABLE tasks DROP COLUMN has_children, DROP COLUMN precise_progress;
ALTER TABLE projects DROP COLUMN has_tasks, DROP COLUMN progress, DROP COLUMN precise_progress;
