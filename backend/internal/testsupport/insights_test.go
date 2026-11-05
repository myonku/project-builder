package testsupport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInsightsBrowser(t *testing.T) {
	if os.Getenv("INSIGHTS_BROWSER") != "1" {
		t.Skip("set INSIGHTS_BROWSER=1 for real browser integration")
	}
	h := NewHarness(t)
	a := h.RegisterAndLogin(t, "insights_browser")
	group := h.CreateGroup(t, a, "周期汇报验收组")
	project := createProjectWithVisibility(t, h, a, group, "平台交付项目", "group_editable")
	for i := 0; i < 8; i++ {
		task := h.CreateTask(t, a, project, "交付事项 "+string(rune('A'+i)), "2026-10-01", "2026-10-10")
		shareTask(t, h, a, task)
		if i < 4 {
			c := a.Client.Get(URL("/tasks/" + task))
			a.Client.Patch(URL("/tasks/"+task), map[string]any{"progress": 100, "expected_updated_at": Str(c.Data(t), "updated_at")}).RequireStatus(t, 200)
		}
	}
	frontend, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Router.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(frontend, "dist", filepath.Clean("/"+r.URL.Path))
		if info, e := os.Stat(path); e != nil || info.IsDir() {
			path = filepath.Join(frontend, "dist", "index.html")
		}
		http.ServeFile(w, r, path)
	}))
	defer server.Close()
	cmd := exec.Command("node", "scripts/insights-browser.mjs", server.URL)
	cmd.Dir = frontend
	cmd.Env = append(os.Environ(), "INSIGHTS_GROUP="+group, "INSIGHTS_COOKIE="+a.CookieValue, "INSIGHTS_COOKIE_NAME="+h.Config.Security.SessionCookieName)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}

func TestInsightsAtomicReplayAndPermissions(t *testing.T) {
	h := NewHarness(t)
	a := h.RegisterAndLogin(t, "insights_a")
	b := h.RegisterAndLogin(t, "insights_b")
	group := h.CreateGroup(t, a, "报告组")
	project := createProjectWithVisibility(t, h, a, group, "报告项目", "group_editable")
	task := h.CreateTask(t, a, project, "可追溯成果", "2026-10-01", "2026-10-10")
	shareTask(t, h, a, task)
	update := func(progress int) {
		current := a.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200)
		a.Client.Patch(URL("/tasks/"+task), map[string]any{"progress": progress, "expected_updated_at": Str(current.Data(t), "updated_at")}).RequireStatus(t, 200)
	}
	update(100)
	update(50)
	update(100)
	now := time.Now().UTC()
	query := "?scope=group&group_id=" + group + "&timezone=UTC&from=" + now.AddDate(0, 0, -1).Format("2006-01-02") + "&to=" + now.Format("2006-01-02")
	response := a.Client.Get(URL("/insights/reports"+query)).RequireStatus(t, 200)
	var report struct {
		Metrics struct{ Completed, Reopened, Open int }
		Events  []any
	}
	raw, _ := json.Marshal(response.Data(t))
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Metrics.Completed != 1 || report.Metrics.Reopened != 1 || report.Metrics.Open != 0 {
		t.Fatalf("wrong metrics: %s", raw)
	}
	b.Client.Get(URL("/insights/reports"+query)).RequireStatus(t, 404)
	a.Client.Get(URL("/insights/reports?scope=self&member_id="+b.UserID)).RequireStatus(t, 400)
	a.Client.Get(URL("/insights/reports?unknown=true")).RequireStatus(t, 400)
	a.Client.Get(URL("/insights/reports?from=2026-02-30")).RequireStatus(t, 400)
	a.Client.Get(URL("/insights/activity"+query+"&limit=2")).RequireStatus(t, 200)
	items := a.Client.Get(URL("/insights/report-items"+query+"&metric=completed")).RequireStatus(t, 200).Data(t)
	if items["total"] != float64(1) {
		t.Fatal(items)
	}
	ctx := context.Background()
	var before, after int
	h.Pool.QueryRow(ctx, `SELECT count(*) FROM insight_events WHERE task_id=$1`, task).Scan(&before)
	tx, _ := h.Pool.Begin(ctx)
	tx.Exec(ctx, `UPDATE tasks SET progress=0,status='not_started' WHERE id=$1`, task)
	tx.Rollback(ctx)
	h.Pool.QueryRow(ctx, `SELECT count(*) FROM insight_events WHERE task_id=$1`, task).Scan(&after)
	if before != after {
		t.Fatal("rollback emitted event")
	}
	// Deferred captures include task members and final effective state in the creating transaction.
	var memberCount int
	err := h.Pool.QueryRow(ctx, `SELECT jsonb_array_length(after_state->'members') FROM insight_events WHERE task_id=$1 AND before_state IS NULL`, task).Scan(&memberCount)
	if err != nil || memberCount != 1 {
		t.Fatal("creation not atomic with membership", memberCount, err)
	}
	// Sharing removal removes all historical contribution from this group projection.
	current := a.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200)
	a.Client.Patch(URL("/tasks/"+task), map[string]any{"is_shared": false, "expected_updated_at": Str(current.Data(t), "updated_at")}).RequireStatus(t, 200)
	result := a.Client.Get(URL("/insights/reports"+query)).RequireStatus(t, 200).Data(t)
	m := result["metrics"].(map[string]any)
	if m["completed"] != float64(0) {
		t.Fatal("unshared history leaked", result)
	}
}
