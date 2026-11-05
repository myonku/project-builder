// Package insights provides permission-scoped current and historical business projections.
package insights

import (
	"encoding/json"
	"sort"
	"time"
)

type Member struct {
	ID       string `json:"user_id"`
	Name     string `json:"display_name"`
	Username string `json:"username"`
}
type State struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	GroupID     string   `json:"group_id"`
	Name        string   `json:"name"`
	ProjectName string   `json:"project_name"`
	Status      string   `json:"status"`
	Progress    int      `json:"progress"`
	StartDate   string   `json:"start_date"`
	EndDate     string   `json:"end_date"`
	HasChildren bool     `json:"has_children"`
	Deleted     bool     `json:"deleted"`
	Shared      bool     `json:"is_shared"`
	Visibility  string   `json:"visibility"`
	Archived    bool     `json:"archived"`
	Milestone   bool     `json:"is_milestone"`
	Priority    int      `json:"priority"`
	Members     []Member `json:"members"`
	CanEdit     bool     `json:"can_edit"`
}

func (s State) Open() bool { return s.Status == "not_started" || s.Status == "in_progress" }

type Event struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"occurred_at"`
	ActorID   *string   `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	Before    *State    `json:"before"`
	After     State     `json:"after"`
	Baseline  bool      `json:"-"`
	Kind      string    `json:"kind"`
}
type Filter struct {
	Scope           string `json:"scope"`
	GroupID         string `json:"group_id"`
	MemberID        string `json:"member_id"`
	ProjectID       string `json:"project_id"`
	Timezone        string `json:"timezone"`
	From            string `json:"from"`
	To              string `json:"to"`
	Bucket          string `json:"bucket"`
	IncludeArchived bool   `json:"include_archived"`
}

func (f Filter) matches(s *State) bool {
	if s == nil || s.Deleted || s.HasChildren {
		return false
	}
	if f.ProjectID != "" && s.ProjectID != f.ProjectID {
		return false
	}
	if f.Scope == "group" || f.Scope == "member" {
		if s.GroupID != f.GroupID || !s.Shared || s.Visibility == "private" {
			return false
		}
	}
	if f.Scope == "self" || f.Scope == "member" {
		for _, m := range s.Members {
			if m.ID == f.MemberID {
				return true
			}
		}
		return false
	}
	return true
}
func classify(e Event) string {
	if e.Baseline {
		return "baseline"
	}
	if e.Before == nil {
		return "created"
	}
	a, b := e.After, *e.Before
	if a.Deleted && !b.Deleted {
		return "deleted"
	}
	if b.Deleted && !a.Deleted {
		return "restored"
	}
	if a.HasChildren != b.HasChildren {
		return "structure"
	}
	if a.Status != b.Status {
		if a.Status == "completed" {
			return "completed"
		}
		if b.Status == "completed" && a.Open() {
			return "reopened"
		}
		if a.Status == "cancelled" {
			return "cancelled"
		}
		return "status"
	}
	if a.EndDate != b.EndDate || a.StartDate != b.StartDate {
		return "rescheduled"
	}
	x, _ := json.Marshal(a.Members)
	y, _ := json.Marshal(b.Members)
	if string(x) != string(y) {
		return "members"
	}
	if a.Shared != b.Shared || a.Visibility != b.Visibility || a.GroupID != b.GroupID {
		return "sharing"
	}
	return "updated"
}

type Metrics struct {
	Created   int `json:"created"`
	Completed int `json:"completed"`
	Reopened  int `json:"reopened"`
	Open      int `json:"open"`
	Overdue   int `json:"overdue"`
}
type Point struct {
	Date      string `json:"date"`
	Until     string `json:"until"`
	Created   int    `json:"created"`
	Completed int    `json:"completed"`
	Open      *int   `json:"open"`
}
type Breakdown struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Completed int    `json:"completed"`
	Open      int    `json:"open"`
}
type Coverage struct {
	Start    time.Time `json:"coverage_start"`
	Complete bool      `json:"complete"`
	Gaps     []string  `json:"gaps"`
}
type Report struct {
	Filter        Filter      `json:"filter"`
	AsOf          time.Time   `json:"as_of"`
	ObservedUntil time.Time   `json:"observed_until"`
	Coverage      Coverage    `json:"coverage"`
	Metrics       Metrics     `json:"metrics"`
	Series        []Point     `json:"series"`
	Members       []Breakdown `json:"members"`
	Projects      []Breakdown `json:"projects"`
	Achievements  []State     `json:"achievements"`
	Attention     []State     `json:"attention"`
	Next          []State     `json:"next"`
	Events        []Event     `json:"-"`
	Previous      *Metrics    `json:"previous"`
	Policy        string      `json:"policy"`
}

func midnight(date string, loc *time.Location) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", date, loc)
	return t
}
func buildReport(f Filter, events []Event, current []State, coverage, asOf time.Time) Report {
	loc, _ := time.LoadLocation(f.Timezone)
	start := midnight(f.From, loc)
	end := midnight(f.To, loc).AddDate(0, 0, 1)
	until := end
	if asOf.Before(until) {
		until = asOf
	}
	r := Report{Filter: f, AsOf: asOf, ObservedUntil: until, Coverage: Coverage{Start: coverage, Complete: !start.Before(coverage), Gaps: []string{}},
		Series: []Point{}, Members: []Breakdown{}, Projects: []Breakdown{}, Achievements: []State{}, Attention: []State{}, Next: []State{}, Events: []Event{}, Policy: "历史按当前可访问范围统计；归档贡献保留；多人参与分别计数。"}
	if !r.Coverage.Complete {
		r.Coverage.Gaps = append(r.Coverage.Gaps, "记录启用前的历史不可还原")
	}
	states := map[string]State{}
	created, completed, reopened := map[string]bool{}, map[string]bool{}, map[string]bool{}
	memberRows, projectRows := map[string]*Breakdown{}, map[string]*Breakdown{}
	memberDone, projectDone := map[string]map[string]bool{}, map[string]map[string]bool{}
	row := func(rows map[string]*Breakdown, id, name string) *Breakdown {
		if rows[id] == nil {
			rows[id] = &Breakdown{ID: id, Name: name}
		}
		return rows[id]
	}
	index := 0
	for day := start; day.Before(end); {
		next := day.AddDate(0, 0, 1)
		if f.Bucket == "week" {
			next = day.AddDate(0, 0, 7-int((day.Weekday()+6)%7))
		}
		if next.After(end) {
			next = end
		}
		bucketEnd := next
		if until.Before(bucketEnd) {
			bucketEnd = until
		}
		p := Point{Date: day.Format("2006-01-02"), Until: next.AddDate(0, 0, -1).Format("2006-01-02")}
		bc, bd := map[string]bool{}, map[string]bool{}
		for index < len(events) && events[index].At.Before(bucketEnd) {
			e := events[index]
			index++
			states[e.After.ID] = e.After
			if e.Baseline || e.At.Before(start) || !f.matches(&e.After) {
				continue
			}
			e.Kind = classify(e)
			if e.Before == nil {
				created[e.After.ID] = true
				bc[e.After.ID] = true
			}
			if e.Before != nil && !e.Before.HasChildren && !e.Before.Deleted && e.Before.Status != "completed" && e.After.Status == "completed" {
				completed[e.After.ID] = true
				bd[e.After.ID] = true
				for _, m := range e.After.Members {
					if memberDone[m.ID] == nil {
						memberDone[m.ID] = map[string]bool{}
					}
					memberDone[m.ID][e.After.ID] = true
					row(memberRows, m.ID, m.Name)
				}
				if projectDone[e.After.ProjectID] == nil {
					projectDone[e.After.ProjectID] = map[string]bool{}
				}
				projectDone[e.After.ProjectID][e.After.ID] = true
				row(projectRows, e.After.ProjectID, e.After.ProjectName)
			}
			if e.Before != nil && e.Before.Status == "completed" && e.After.Open() && !e.Before.HasChildren {
				reopened[e.After.ID] = true
			}
			r.Events = append(r.Events, e)
		}
		p.Created = len(bc)
		p.Completed = len(bd)
		if !day.After(asOf) && !day.Before(coverage) {
			n := 0
			for _, s := range states {
				if f.matches(&s) && s.Open() {
					n++
				}
			}
			p.Open = &n
		}
		r.Series = append(r.Series, p)
		day = next
	}
	for _, s := range states {
		if !f.matches(&s) {
			continue
		}
		if s.Open() {
			r.Metrics.Open++
			if s.EndDate < until.In(loc).Add(-time.Nanosecond).Format("2006-01-02") {
				r.Metrics.Overdue++
				r.Attention = append(r.Attention, s)
			}
			if f.Scope == "group" {
				for _, m := range s.Members {
					row(memberRows, m.ID, m.Name).Open++
				}
				if len(s.Members) == 0 {
					row(memberRows, "unassigned", "未分配").Open++
				}
			}
			row(projectRows, s.ProjectID, s.ProjectName).Open++
		}
		if completed[s.ID] {
			r.Achievements = append(r.Achievements, s)
		}
	}
	// Next-period plans intentionally use current facts, not a historical commitment baseline.
	nextEnd := end.AddDate(0, 0, calendarDays(start, end))
	if start.Day() == 1 && end.Day() == 1 {
		nextEnd = end.AddDate(0, (end.Year()-start.Year())*12+int(end.Month()-start.Month()), 0)
	}
	for _, s := range current {
		if f.matches(&s) && s.Open() && s.EndDate >= end.Format("2006-01-02") && s.EndDate < nextEnd.Format("2006-01-02") {
			r.Next = append(r.Next, s)
		}
	}
	r.Metrics.Created = len(created)
	r.Metrics.Completed = len(completed)
	r.Metrics.Reopened = len(reopened)
	for id, v := range memberRows {
		v.Completed = len(memberDone[id])
		r.Members = append(r.Members, *v)
	}
	for id, v := range projectRows {
		v.Completed = len(projectDone[id])
		r.Projects = append(r.Projects, *v)
	}
	sortBreak := func(x []Breakdown) {
		sort.Slice(x, func(i, j int) bool {
			if x[i].Open != x[j].Open {
				return x[i].Open > x[j].Open
			}
			return x[i].ID < x[j].ID
		})
	}
	sortBreak(r.Members)
	sortBreak(r.Projects)
	for _, list := range [][]State{r.Achievements, r.Attention, r.Next} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].EndDate != list[j].EndDate {
				return list[i].EndDate < list[j].EndDate
			}
			return list[i].ID < list[j].ID
		})
	}
	sort.Slice(r.Events, func(i, j int) bool { return r.Events[i].ID > r.Events[j].ID })
	return r
}

func sortStates(items []State) {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

// Calendar intervals must not inherit a 23/25-hour DST day as their duration.
func calendarDays(a, b time.Time) int {
	x, _ := time.Parse("2006-01-02", a.Format("2006-01-02"))
	y, _ := time.Parse("2006-01-02", b.Format("2006-01-02"))
	return int(y.Sub(x).Hours() / 24)
}
