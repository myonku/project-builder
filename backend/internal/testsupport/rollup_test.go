package testsupport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/database"
	"github.com/myonku/project-builder/internal/domain"
)

func TestHierarchicalRollup(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "rollup")
	group := h.CreateGroup(t, owner, "rollup")
	project := h.CreateProject(t, owner, group, "rollup")
	create := func(name string, parent any) string {
		t.Helper()
		return Str(owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{
			"name": name, "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-20",
		}).RequireStatus(t, 201).Data(t), "id")
	}
	get := func(id string) map[string]any {
		t.Helper()
		return owner.Client.Get(URL("/tasks/"+id)).RequireStatus(t, 200).Data(t)
	}
	update := func(id string, fields map[string]any) {
		t.Helper()
		fields["expected_updated_at"] = get(id)["updated_at"]
		owner.Client.Patch(URL("/tasks/"+id), fields).RequireStatus(t, 200)
	}
	check := func(path, status string, progress int) map[string]any {
		t.Helper()
		data := owner.Client.Get(URL(path)).RequireStatus(t, 200).Data(t)
		if Str(data, "status") != status || Int(data, "progress") != progress {
			t.Fatalf("%s: %v; want %s/%d", path, data, status, progress)
		}
		return data
	}
	a := create("A", nil)
	b := create("B", nil)
	c := create("A1", a)
	d := create("A2", a)
	update(d, map[string]any{"progress": 100})
	update(b, map[string]any{"progress": 100})
	check("/tasks/"+a, "in_progress", 50)
	check("/projects/"+project, "in_progress", 75)
	parent := get(a)
	if parent["has_children"] != true {
		t.Fatal("parent must advertise rollup")
	}
	owner.Client.Patch(URL("/tasks/"+a), map[string]any{"progress": 50, "expected_updated_at": parent["updated_at"]}).RequireStatus(t, 422)
	p := check("/projects/"+project, "in_progress", 75)
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"status": "paused", "expected_updated_at": p["updated_at"]}).RequireStatus(t, 422)
	// A third layer must preserve the weight of each immediate branch.
	e := create("A11", c)
	f := create("A12", c)
	g := create("A13", c)
	update(e, map[string]any{"progress": 1})
	check("/tasks/"+c, "in_progress", 0)
	check("/tasks/"+a, "in_progress", 50)
	check("/projects/"+project, "in_progress", 75)
	update(e, map[string]any{"progress": 100})
	update(f, map[string]any{"progress": 100})
	update(g, map[string]any{"progress": 99})
	// 99.666... -> 99.833... -> 99.916..., display 100 but still in progress.
	check("/tasks/"+a, "in_progress", 100)
	check("/projects/"+project, "in_progress", 100)
	update(g, map[string]any{"progress": 100})
	check("/projects/"+project, "completed", 100)
	update(c, map[string]any{"name": "renamed parent"})
	for _, id := range []string{e, f, g} {
		update(id, map[string]any{"status": "cancelled"})
	}
	check("/tasks/"+c, "cancelled", 0)
	check("/tasks/"+a, "completed", 100)
	update(d, map[string]any{"status": "cancelled"})
	check("/tasks/"+a, "cancelled", 0)
	check("/projects/"+project, "completed", 100)
	update(b, map[string]any{"status": "cancelled"})
	check("/projects/"+project, "cancelled", 0)
	// All-cancelled structural children never turn their parent into an editable leaf.
	owner.Client.Patch(URL("/tasks/"+a), map[string]any{"status": "not_started", "expected_updated_at": get(a)["updated_at"]}).RequireStatus(t, 422)
	// Restore/delete is supported by storage even though no restore API is exposed.
	_, err := h.Pool.Exec(context.Background(), `UPDATE tasks SET deleted_at=now() WHERE parent_id=$1`, c)
	if err != nil {
		t.Fatal(err)
	}
	if get(c)["has_children"] != false {
		t.Fatal("last child deletion must restore manual mode")
	}
	update(c, map[string]any{"progress": 60})
	check("/tasks/"+a, "in_progress", 60)
	_, err = h.Pool.Exec(context.Background(), `UPDATE tasks SET deleted_at=NULL WHERE parent_id=$1`, c)
	if err != nil {
		t.Fatal(err)
	}
	check("/tasks/"+c, "cancelled", 0)
}

func TestRollupManualProjectAndTransitions(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "manual")
	group := h.CreateGroup(t, owner, "manual")
	project := h.CreateProject(t, owner, group, "manual")
	detail := func() map[string]any {
		return owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	}
	p := detail()
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"progress": 40, "expected_updated_at": p["updated_at"]}).RequireStatus(t, 200)
	if p = detail(); Int(p, "progress") != 40 || Str(p, "status") != "in_progress" || p["has_tasks"] != false {
		t.Fatal(p)
	}
	task := h.CreateTask(t, owner, project, "first", "2026-10-01", "2026-10-10")
	if p = detail(); Int(p, "progress") != 0 || p["has_tasks"] != true {
		t.Fatal(p)
	}
	data := owner.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200).Data(t)
	owner.Client.Patch(URL("/tasks/"+task), map[string]any{"progress": 70, "expected_updated_at": data["updated_at"]}).RequireStatus(t, 200)
	data = owner.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200).Data(t)
	owner.Client.Delete(URL("/tasks/"+task), map[string]any{"expected_updated_at": data["updated_at"]}).RequireStatus(t, 204)
	if p = detail(); Int(p, "progress") != 70 || p["has_tasks"] != false {
		t.Fatal(p)
	}
	owner.Client.Post(URL("/projects/"+project+"/archive"), map[string]any{"expected_updated_at": p["updated_at"]}).RequireStatus(t, 200)
	p = detail()
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"progress": 20, "expected_updated_at": p["updated_at"]}).RequireStatus(t, 409)
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"status": "in_progress", "name": "bypass", "expected_updated_at": p["updated_at"]}).RequireStatus(t, 409)
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"status": "in_progress", "expected_updated_at": p["updated_at"]}).RequireStatus(t, 200)
	if p = detail(); p["archived_at"] != nil {
		t.Fatal("unarchive must clear timestamp", p)
	}
}

func TestRollupConcurrentWriters(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "concurrentrollup")
	group := h.CreateGroup(t, owner, "concurrent")
	project := h.CreateProject(t, owner, group, "concurrent")
	parent := h.CreateTask(t, owner, project, "parent", "2026-10-01", "2026-10-20")
	createChild := func(name string) string {
		return Str(owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": name, "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-10"}).RequireStatus(t, 201).Data(t), "id")
	}
	a := createChild("a")
	b := createChild("b")
	var wg sync.WaitGroup
	for _, id := range []string{a, b} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			data := owner.Client.Get(URL("/tasks/"+id)).RequireStatus(t, 200).Data(t)
			owner.Client.Patch(URL("/tasks/"+id), map[string]any{"progress": 100, "expected_updated_at": data["updated_at"]}).RequireStatus(t, 200)
		}(id)
	}
	wg.Wait()
	p := owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	if Int(p, "progress") != 100 || Str(p, "status") != "completed" {
		t.Fatal("lost concurrent update", p)
	}
	// An outdated parent version cannot overwrite the aggregate after a child is added.
	old := owner.Client.Get(URL("/tasks/"+parent)).RequireStatus(t, 200).Data(t)
	createChild("third")
	response := owner.Client.Patch(URL("/tasks/"+parent), map[string]any{"name": "stale", "expected_updated_at": old["updated_at"]})
	response.RequireStatus(t, 409)
}

func TestRollupLegacyMigrationRollback(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "legacyrollup")
	group := h.CreateGroup(t, owner, "legacy")
	project := h.CreateProject(t, owner, group, "legacy")
	a := h.CreateTask(t, owner, project, "parent", "2026-10-01", "2026-10-20")
	b := h.CreateTask(t, owner, project, "child", "2026-10-01", "2026-10-10")
	migrator, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()
	if err = migrator.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(context.Background(), `UPDATE tasks SET progress=80,status='in_progress' WHERE id=$1;`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(context.Background(), `UPDATE tasks SET parent_id=$1, progress=20,status='in_progress' WHERE id=$2`, a, b); err != nil {
		t.Fatal(err)
	}
	if err = migrator.Up(); err != nil {
		t.Fatal(err)
	}
	var value int
	if err = h.Pool.QueryRow(context.Background(), `SELECT progress FROM tasks WHERE id=$1`, a).Scan(&value); err != nil || value != 20 {
		t.Fatal(value, err)
	}
	if err = migrator.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if err = h.Pool.QueryRow(context.Background(), `SELECT progress FROM tasks WHERE id=$1`, a).Scan(&value); err != nil || value != 80 {
		t.Fatal("legacy manual value not restored", value, err)
	}
	if err = migrator.Up(); err != nil {
		t.Fatal(err)
	}
}

func TestRollupHiddenBranchesAndQueryConsistency(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "treeowner")
	viewer := h.RegisterAndLogin(t, "treeviewer")
	group := h.CreateGroup(t, owner, "sharedtree")
	h.AddGroupMember(t, owner, group, viewer.UserID)
	project := h.CreateProject(t, owner, group, "sharedtree")
	getTask := func(actor Authenticated, id string) map[string]any {
		return actor.Client.Get(URL("/tasks/"+id)).RequireStatus(t, 200).Data(t)
	}
	p := owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	owner.Client.Patch(URL("/projects/"+project), map[string]any{"visibility": "group_editable", "expected_updated_at": p["updated_at"]}).RequireStatus(t, 200)
	create := func(name string, parent any, shared bool) string {
		return Str(owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": name, "parent_id": parent, "is_shared": shared, "start_date": "2026-10-01", "end_date": "2026-10-20"}).RequireStatus(t, 201).Data(t), "id")
	}
	parent := create("shared parent", nil, true)
	hidden := create("hidden secret", parent, false)
	visible := create("visible leaf", parent, true)
	owner.Client.Patch(URL("/tasks/"+hidden), map[string]any{"progress": 100, "expected_updated_at": getTask(owner, hidden)["updated_at"]}).RequireStatus(t, 200)
	if Int(getTask(viewer, parent), "progress") != 50 {
		t.Fatal("hidden branch must contribute to parent's aggregate")
	}
	viewer.Client.Get(URL("/tasks/"+hidden)).RequireStatus(t, 404)
	response := viewer.Client.Post(URL("/projects/"+project+"/tasks/query"), map[string]any{"page_size": 1, "sort": "progress"}).RequireStatus(t, 200)
	if Int(response.Pagination(t), "total") != 2 {
		t.Fatal("hidden task leaked into count")
	}
	for _, row := range response.ListData(t) {
		if Str(row, "id") == hidden {
			t.Fatal("hidden child leaked")
		}
	}
	viewer.Client.Patch(URL("/tasks/"+visible), map[string]any{"progress": 100, "expected_updated_at": getTask(viewer, visible)["updated_at"]}).RequireStatus(t, 200)
	if parentData := getTask(viewer, parent); Str(parentData, "status") != "completed" || Str(parentData, "updated_by") != viewer.UserID {
		t.Fatal("ancestor result/actor not propagated", parentData)
	}
	// Seeing/editing one shared leaf does not confer project management authority.
	p = viewer.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	viewer.Client.Patch(URL("/projects/"+project), map[string]any{"name": "forbidden", "expected_updated_at": p["updated_at"]}).RequireStatus(t, 403)
	for _, page := range []int{1, 2} {
		result := viewer.Client.Post(URL("/projects/"+project+"/tasks/query"), map[string]any{"status": "completed", "page_size": 1, "page": page, "sort": "progress"}).RequireStatus(t, 200)
		if Int(result.Pagination(t), "total") != 2 || len(result.ListData(t)) != 1 {
			t.Fatal("effective status pagination is inconsistent")
		}
	}
	gantt := viewer.Client.Get(URL("/plan-views/projects/"+project+"/gantt")).RequireStatus(t, 200).Data(t)
	if Int(gantt["project"].(map[string]any), "progress") != 100 {
		t.Fatal("gantt disagrees with project")
	}
	filtered := viewer.Client.Get(URL("/plan-views/groups/"+group+"?task_status=not_started")).RequireStatus(t, 200).Data(t)
	for _, raw := range filtered["projects"].([]any) {
		item := raw.(map[string]any)
		if Str(item, "id") == project && Int(item, "progress") != 100 {
			t.Fatal("filter changed aggregate", item)
		}
	}
}

func TestRollupReparentAndStructureConstraints(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "treemove")
	group := h.CreateGroup(t, owner, "move")
	project := h.CreateProject(t, owner, group, "move")
	create := func(name string, parent any) string {
		return Str(owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": name, "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-20"}).RequireStatus(t, 201).Data(t), "id")
	}
	get := func(id string) map[string]any {
		return owner.Client.Get(URL("/tasks/"+id)).RequireStatus(t, 200).Data(t)
	}
	patch := func(id string, body map[string]any, status int) {
		body["expected_updated_at"] = get(id)["updated_at"]
		owner.Client.Patch(URL("/tasks/"+id), body).RequireStatus(t, status)
	}
	a := create("a", nil)
	b := create("b", nil)
	c := create("c", a)
	d := create("d", a)
	patch(d, map[string]any{"progress": 100}, 200)
	patch(c, map[string]any{"parent_id": b}, 200)
	p := owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	if Int(get(a), "progress") != 100 || Int(get(b), "progress") != 0 || Int(p, "progress") != 50 {
		t.Fatal("old/new ancestors not updated", p)
	}
	patch(c, map[string]any{"parent_id": nil}, 200)
	if get(b)["has_children"] != false {
		t.Fatal("last child move must restore manual mode")
	}
	third := create("third", d)
	patch(a, map[string]any{"parent_id": third}, 422)
	patch(a, map[string]any{"parent_id": b}, 422) // Moving a subtree would create a fourth level.
	patch(a, map[string]any{"end_date": "2026-10-10"}, 422)
	// Deleting a parent excludes that row, while its surviving child becomes an effective root.
	owner.Client.Delete(URL("/tasks/"+a), map[string]any{"expected_updated_at": get(a)["updated_at"]}).RequireStatus(t, 204)
	p = owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t)
	if p["has_tasks"] != true {
		t.Fatal("surviving descendants must still contribute")
	}
}

func TestRollupFirstChildRacesManualSave(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "firstchildrace")
	group := h.CreateGroup(t, owner, "race")
	project := h.CreateProject(t, owner, group, "race")
	for range 5 {
		parent := h.CreateTask(t, owner, project, "parent", "2026-10-01", "2026-10-20")
		before := owner.Client.Get(URL("/tasks/"+parent)).RequireStatus(t, 200).Data(t)
		start := make(chan struct{})
		results := make(chan Response, 2)
		go func() {
			<-start
			results <- owner.Client.Patch(URL("/tasks/"+parent), map[string]any{"progress": 65, "expected_updated_at": before["updated_at"]})
		}()
		go func() {
			<-start
			results <- owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": "first child", "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-20"})
		}()
		close(start)
		for range 2 {
			result := <-results
			if result.Status != 200 && result.Status != 201 && result.Status != 409 && result.Status != 422 {
				t.Fatalf("unexpected race result: %d %s", result.Status, result.Body)
			}
		}
		after := owner.Client.Get(URL("/tasks/"+parent)).RequireStatus(t, 200).Data(t)
		if after["has_children"] != true || Int(after, "progress") != 0 || Str(after, "status") != "not_started" {
			t.Fatal("manual save overrode a non-leaf", after)
		}
	}
}

func TestRollupRemindersUseActualStatus(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "rollupreminder")
	group := h.CreateGroup(t, owner, "reminder")
	project := h.CreateProject(t, owner, group, "reminder")
	parent := h.CreateTask(t, owner, project, "parent", "2026-10-01", "2026-10-05")
	var last string
	for index, progress := range []int{100, 100, 99} {
		child := owner.Client.Post(URL("/projects/"+project+"/tasks"), map[string]any{"name": "leaf", "parent_id": parent, "start_date": "2026-10-01", "end_date": "2026-10-05"}).RequireStatus(t, 201).Data(t)
		id := Str(child, "id")
		owner.Client.Patch(URL("/tasks/"+id), map[string]any{"progress": progress, "expected_updated_at": child["updated_at"]}).RequireStatus(t, 200)
		if index == 2 {
			last = id
		}
	}
	data := owner.Client.Get(URL("/tasks/"+parent)).RequireStatus(t, 200).Data(t)
	if Int(data, "progress") != 100 || Str(data, "status") != "in_progress" || data["is_overdue"] != true {
		t.Fatal("rounded percentage drove terminal state", data)
	}
	if _, err := h.Reminders.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if countReminders(t, h, parent, domain.ReminderKindOverdue) != 1 {
		t.Fatal("rounded 100% suppressed reminder")
	}
	child := owner.Client.Get(URL("/tasks/"+last)).RequireStatus(t, 200).Data(t)
	owner.Client.Patch(URL("/tasks/"+last), map[string]any{"progress": 100, "expected_updated_at": child["updated_at"]}).RequireStatus(t, 200)
	h.Clock.Advance(24 * time.Hour)
	if _, err := h.Reminders.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if countReminders(t, h, parent, domain.ReminderKindOverdue) != 1 {
		t.Fatal("completed parent generated another overdue reminder")
	}
}
