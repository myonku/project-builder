package insights

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/myonku/project-builder/internal/app/planviews"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

type Handler struct {
	pool   *pgxpool.Pool
	actors planviews.ActorLoader
}

func NewHandler(pool *pgxpool.Pool, actors planviews.ActorLoader) *Handler {
	return &Handler{pool, actors}
}
func (h *Handler) Register(api *gin.RouterGroup) {
	for _, name := range []string{"overview", "reports", "activity", "report-items"} {
		api.GET("/insights/"+name, h.read)
	}
}
func invalid(field string) error {
	return domain.ErrInvalidRequest.WithField(field, "查询参数无效")
}
func parse(c *gin.Context, actorID, defaultZone string, now time.Time) (Filter, error) {
	allowed := map[string]bool{}
	for _, k := range []string{"scope", "group_id", "member_id", "project_id", "timezone", "from", "to", "bucket", "include_archived", "cursor", "limit", "metric", "as_of"} {
		allowed[k] = true
	}
	for k, v := range c.Request.URL.Query() {
		if !allowed[k] || len(v) != 1 {
			return Filter{}, invalid(k)
		}
	}
	f := Filter{Scope: c.DefaultQuery("scope", "self"), GroupID: c.Query("group_id"), MemberID: c.Query("member_id"), ProjectID: c.Query("project_id"), Timezone: c.DefaultQuery("timezone", defaultZone), From: c.Query("from"), To: c.Query("to"), Bucket: c.DefaultQuery("bucket", "day")}
	for k, v := range map[string]string{"group_id": f.GroupID, "member_id": f.MemberID, "project_id": f.ProjectID} {
		if v != "" && !idgen.IsUUID(v) {
			return f, invalid(k)
		}
	}
	if f.Scope != "self" && f.Scope != "group" && f.Scope != "member" && f.Scope != "project" && f.Scope != "participating" {
		return f, invalid("scope")
	}
	if (f.Scope == "group" || f.Scope == "member") && f.GroupID == "" {
		return f, invalid("group_id")
	}
	if f.Scope == "member" && f.MemberID == "" {
		return f, invalid("member_id")
	}
	if f.Scope == "project" && f.ProjectID == "" {
		return f, invalid("project_id")
	}
	if f.Scope == "self" {
		if f.MemberID != "" && f.MemberID != actorID {
			return f, invalid("member_id")
		}
		f.MemberID = actorID
	}
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		return f, invalid("timezone")
	}
	day := now.In(loc)
	week := day.AddDate(0, 0, -int((day.Weekday()+6)%7))
	if f.From == "" {
		f.From = week.Format("2006-01-02")
	}
	if f.To == "" {
		f.To = week.AddDate(0, 0, 6).Format("2006-01-02")
	}
	a, err := time.ParseInLocation("2006-01-02", f.From, loc)
	if err != nil {
		return f, invalid("from")
	}
	b, err := time.ParseInLocation("2006-01-02", f.To, loc)
	if err != nil || b.Before(a) || b.After(a.AddDate(0, 0, 366)) || a.After(now) {
		return f, invalid("to")
	}
	if f.Bucket != "day" && f.Bucket != "week" {
		return f, invalid("bucket")
	}
	if v := c.Query("include_archived"); v != "" {
		if v != "true" && v != "false" {
			return f, invalid("include_archived")
		}
		f.IncludeArchived = v == "true"
	}
	return f, nil
}

func (h *Handler) read(c *gin.Context) {
	actor, err := h.actors.LoadActor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	tx, err := h.pool.BeginTx(c.Request.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	ctx := c.Request.Context()
	var now, coverage time.Time
	var zone string
	err = tx.QueryRow(ctx, `SELECT clock_timestamp(),COALESCE(NULLIF(timezone,''),'UTC') FROM users WHERE id=$1`, actor.ID).Scan(&now, &zone)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	f, err := parse(c, actor.ID, zone, now)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	if v := c.Query("as_of"); v != "" {
		t, e := time.Parse(time.RFC3339Nano, v)
		if e != nil || t.After(now) {
			httpapi.Abort(c, invalid("as_of"))
			return
		}
		now = t
	}
	if err = tx.QueryRow(ctx, `SELECT started_at FROM insights_coverage WHERE id`).Scan(&coverage); err != nil {
		httpapi.Abort(c, err)
		return
	}
	current, events, err := load(ctx, tx, actor.ID, f, now)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	r := buildReport(f, events, current, coverage, now)
	loc, _ := time.LoadLocation(f.Timezone)
	start := midnight(f.From, loc)
	end := midnight(f.To, loc).AddDate(0, 0, 1)
	if !end.After(now) {
		pf := f
		previousStart := start.AddDate(0, 0, -calendarDays(start, end))
		if start.Day() == 1 && end.Day() == 1 {
			previousStart = start.AddDate(0, -((end.Year()-start.Year())*12 + int(end.Month()-start.Month())), 0)
		}
		if !previousStart.Before(coverage) {
			pf.From = previousStart.Format("2006-01-02")
			pf.To = start.AddDate(0, 0, -1).Format("2006-01-02")
			previous := buildReport(pf, events, current, coverage, now)
			r.Previous = &previous.Metrics
		}
	}

	path := c.FullPath()
	if path == "/api/v1/insights/reports" {
		for i := range r.Events {
			r.Events[i] = publicEvent(r.Events[i], f)
		}
		httpapi.RespondOK(c, r)
		return
	}
	if path == "/api/v1/insights/overview" {
		visible := []State{}
		status := map[string]int{"not_started": 0, "in_progress": 0, "completed": 0, "cancelled": 0}
		open, overdue, soon := 0, 0, 0
		today := now.In(loc).Format("2006-01-02")
		points := make([]Point, 14)
		for i := range points {
			points[i].Date = now.In(loc).AddDate(0, 0, i).Format("2006-01-02")
		}
		milestones := []State{}
		for _, s := range current {
			if !f.matches(&s) || (!f.IncludeArchived && s.Archived) {
				continue
			}
			visible = append(visible, s)
			status[s.Status]++
			if s.Open() {
				open++
				if s.EndDate < today {
					overdue++
				}
				if s.EndDate >= today && s.EndDate <= now.In(loc).AddDate(0, 0, 7).Format("2006-01-02") {
					soon++
				}
				for i := range points {
					if s.EndDate == points[i].Date {
						points[i].Created++
					}
				}
			}
			if s.Milestone && s.Open() && s.EndDate <= now.In(loc).AddDate(0, 0, 30).Format("2006-01-02") {
				milestones = append(milestones, s)
			}
		}
		members := map[string]Member{}
		projects := map[string]string{}
		for _, task := range current {
			projects[task.ProjectID] = task.ProjectName
			for _, m := range task.Members {
				members[m.ID] = m
			}
		}
		for _, event := range events {
			if f.matches(&event.After) {
				for _, m := range event.After.Members {
					members[m.ID] = m
				}
			}
		}
		memberOptions := []Member{}
		for _, m := range members {
			memberOptions = append(memberOptions, m)
		}
		httpapi.RespondOK(c, gin.H{"member_options": memberOptions, "project_options": projects, "filter": f, "as_of": now, "coverage": r.Coverage, "tasks": visible, "status": status, "open": open, "overdue": overdue, "due_soon": soon, "due": points, "milestones": milestones, "period": r.Metrics})
		return
	}
	limit := 6
	if v := c.Query("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 100 {
			httpapi.Abort(c, invalid("limit"))
			return
		}
	}
	offset := 0
	if v := c.Query("cursor"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			httpapi.Abort(c, invalid("cursor"))
			return
		}
	}
	if path == "/api/v1/insights/activity" {
		list := []Event{}
		for _, e := range events {
			if !e.Baseline && f.matches(&e.After) {
				e.Kind = classify(e)
				list = append([]Event{publicEvent(e, f)}, list...)
			}
		}
		// Strip irrelevant account/audit fields: only authorized business snapshots are returned.
		lo := min(offset, len(list))
		hi := min(lo+limit, len(list))
		next := ""
		if hi < len(list) {
			next = strconv.Itoa(hi)
		}
		httpapi.RespondOK(c, gin.H{"items": list[lo:hi], "next_cursor": next, "as_of": now, "coverage": r.Coverage})
		return
	}
	metric := c.DefaultQuery("metric", "completed")
	items := []State{}
	seen := map[string]bool{}
	if metric == "open" || metric == "overdue" { // period-end state, not current state
		states := map[string]State{}
		for _, e := range events {
			if e.At.Before(r.ObservedUntil) {
				states[e.After.ID] = e.After
			}
		}
		for _, s := range states {
			if f.matches(&s) && s.Open() && (metric == "open" || s.EndDate < r.ObservedUntil.In(loc).Add(-time.Nanosecond).Format("2006-01-02")) {
				items = append(items, s)
			}
		}
	} else if metric == "completed" || metric == "created" || metric == "reopened" {
		for _, e := range r.Events {
			if e.Kind == metric && !seen[e.After.ID] {
				seen[e.After.ID] = true
				items = append(items, e.After)
			}
		}
	} else {
		httpapi.Abort(c, invalid("metric"))
		return
	}
	sortStates(items)
	lo := min(offset, len(items))
	hi := min(lo+limit, len(items))
	next := ""
	if hi < len(items) {
		next = strconv.Itoa(hi)
	}
	httpapi.RespondOK(c, gin.H{"items": items[lo:hi], "total": len(items), "next_cursor": next, "as_of": now, "policy": r.Policy})
}

func load(ctx context.Context, tx pgx.Tx, actorID string, f Filter, asOf time.Time) ([]State, []Event, error) {
	service := planviews.NewService(planviews.Options{Store: planviews.NewSnapshotRepository(tx)})
	filter := planviews.Filter{Project: planviews.ProjectFilter{IncludeArchived: true}}
	if f.ProjectID != "" {
		filter.Project.ProjectID = &f.ProjectID
		filter.Task.ProjectID = &f.ProjectID
	}
	var view planviews.View
	var err error
	if f.Scope == "project" {
		var g planviews.Gantt
		g, err = service.Gantt(ctx, actorID, f.ProjectID)
		view.Tasks = g.Tasks
	} else if f.Scope == "group" || f.Scope == "member" {
		view, err = service.Group(ctx, actorID, f.GroupID, filter)
	} else {
		view, err = service.Personal(ctx, actorID, filter)
	}
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	editable := map[string]bool{}
	for _, t := range view.Tasks {
		ids = append(ids, t.Record.ID)
		editable[t.Record.ID] = t.CanEdit
	}
	current := []State{}
	events := []Event{}
	if len(ids) == 0 {
		return current, events, nil
	}
	rows, err := tx.Query(ctx, `SELECT insight_snapshot(id) FROM tasks WHERE id=ANY($1::uuid[]) ORDER BY end_date,id`, ids)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, nil, err
		}
		var s State
		if err = json.Unmarshal(raw, &s); err != nil {
			rows.Close()
			return nil, nil, err
		}
		s.CanEdit = editable[s.ID]
		current = append(current, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `SELECT e.id,e.occurred_at,e.actor_id,COALESCE(u.display_name,'系统'),e.before_state,e.after_state,e.baseline
 FROM insight_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.task_id=ANY($1::uuid[]) AND e.occurred_at<=$2 ORDER BY e.occurred_at,e.id`, ids, asOf)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var before, after []byte
		if err = rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorName, &before, &after, &e.Baseline); err != nil {
			return nil, nil, err
		}
		if len(before) > 0 {
			var b State
			if err = json.Unmarshal(before, &b); err != nil {
				return nil, nil, err
			}
			e.Before = &b
		}
		if err = json.Unmarshal(after, &e.After); err != nil {
			return nil, nil, err
		}
		e.Kind = classify(e)
		events = append(events, e)
	}
	return current, events, rows.Err()
}

// Sharing an object today does not expose its previously private state.
func publicEvent(e Event, f Filter) Event {
	if e.Before != nil && (f.Scope == "group" || f.Scope == "member") &&
		(!e.Before.Shared || e.Before.Visibility == "private" || e.Before.GroupID != f.GroupID) {
		e.Before = nil
	}
	return e
}
