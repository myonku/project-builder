package insights

import (
	"fmt"
	"testing"
	"time"
)

func TestReplayCompletionReopenMembershipAndCoverage(t *testing.T) {
	at := func(v string) time.Time { x, _ := time.Parse(time.RFC3339, v); return x }
	f := Filter{Scope: "group", GroupID: "g", From: "2026-11-02", To: "2026-11-08", Timezone: "Asia/Shanghai", Bucket: "day"}
	s := State{ID: "task", ProjectID: "p", GroupID: "g", Status: "not_started", EndDate: "2026-11-07", Visibility: "group_editable", Shared: true, Members: []Member{{ID: "a"}, {ID: "b"}}}
	clone := func(status string) State { x := s; x.Status = status; return x }
	active, done := clone("in_progress"), clone("completed")
	events := []Event{{ID: 1, At: at("2026-11-01T01:00:00Z"), After: s, Baseline: true}, {ID: 2, At: at("2026-11-03T01:00:00Z"), Before: &s, After: done}, {ID: 3, At: at("2026-11-04T01:00:00Z"), Before: &done, After: active}, {ID: 4, At: at("2026-11-05T01:00:00Z"), Before: &active, After: done}}
	r := buildReport(f, events, []State{done}, at("2026-11-01T00:00:00Z"), at("2026-11-10T00:00:00Z"))
	if r.Metrics.Completed != 1 || r.Metrics.Reopened != 1 || r.Metrics.Open != 0 || len(r.Members) != 2 {
		t.Fatalf("bad replay: %+v", r)
	}
	total := 0
	for _, p := range r.Series {
		total += p.Completed
	}
	if total != 2 {
		t.Fatal(total)
	}
	f.Scope = "member"
	f.MemberID = "new"
	r = buildReport(f, events, nil, at("2026-11-01T00:00:00Z"), at("2026-11-10T00:00:00Z"))
	if r.Metrics.Completed != 0 {
		t.Fatal("attributed before membership")
	}
	f.Scope = "group"
	r = buildReport(f, events, nil, at("2026-11-04T00:00:00Z"), at("2026-11-06T00:00:00Z"))
	if r.Coverage.Complete || r.Series[0].Open != nil || r.Series[6].Open != nil {
		t.Fatal("unknown/future points must be null")
	}
	parent := done
	parent.HasChildren = true
	events[1].After = parent
	r = buildReport(f, events[:2], nil, at("2026-11-01T00:00:00Z"), at("2026-11-10T00:00:00Z"))
	if r.Metrics.Completed != 0 {
		t.Fatal("parent counted")
	}
}

func TestRepresentativeReplayScale(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	events := make([]Event, 100000)
	for i := range events {
		s := State{ID: fmt.Sprint(i % 1000), ProjectID: "p", GroupID: "g", Status: "in_progress", EndDate: "2026-12-01", Shared: true, Visibility: "group_editable", Members: []Member{{ID: fmt.Sprint(i % 100)}}}
		b := s
		events[i] = Event{ID: int64(i + 1), At: start.Add(time.Duration(i) * (90 * 24 * time.Hour / 100000)), Before: &b, After: s}
	}
	begin := time.Now()
	r := buildReport(Filter{Scope: "group", GroupID: "g", From: "2026-10-01", To: "2026-10-30", Timezone: "UTC", Bucket: "week"}, events, nil, start, start.AddDate(0, 3, 0))
	if r.Metrics.Open != 1000 || len(r.Members) != 100 {
		t.Fatal(r.Metrics, len(r.Members))
	}
	t.Logf("100000 events / 1000 tasks / 100 members / 90 days: %s", time.Since(begin))
}
