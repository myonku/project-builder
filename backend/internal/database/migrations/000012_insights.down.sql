DROP TRIGGER insight_project_changed ON projects;
DROP TRIGGER insight_members_changed ON task_members;
DROP TRIGGER insight_task_changed ON tasks;
DROP FUNCTION insight_project_changed();
DROP FUNCTION insight_task_changed();
DROP FUNCTION capture_insight(uuid);
DROP FUNCTION insight_snapshot(uuid);
DROP TABLE insight_events,insight_states,insights_coverage;
