package testsupport

import (
	"context"
	"sync"
	"testing"

	"github.com/myonku/project-builder/internal/database"
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
