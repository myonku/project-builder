package testsupport

import (
	"context"
	"testing"
	"time"
)

func TestP02ScheduleRules(t *testing.T) {
	pool := EnsureMigrated(t)
	for _, tc := range []struct {
		name, kind, status, start, end, tz, now, want string
		immediate                                     bool
	}{
		{"Monday uses Friday", "task_due_soon", "not_started", "2026-11-01", "2026-11-02", "Asia/Shanghai", "2026-10-29T00:00:00Z", "2026-10-30T01:00:00Z", false},
		{"due today can catch up", "task_due_soon", "not_started", "2026-10-01", "2026-10-29", "UTC", "2026-10-29T12:00:00Z", "2026-10-28T09:00:00Z", false},
		{"due yesterday cannot", "task_due_soon", "in_progress", "2026-10-01", "2026-10-28", "UTC", "2026-10-29T00:01:00Z", "", false},
		{"natural overdue stays nine", "task_overdue", "in_progress", "2026-10-01", "2026-10-28", "UTC", "2026-10-29T00:01:00Z", "2026-10-29T09:00:00Z", false},
		{"new overdue immediate", "task_overdue", "not_started", "2026-10-01", "2026-10-28", "UTC", "2026-10-29T00:01:00Z", "2026-10-29T00:01:00Z", true},
		{"start passed", "task_start_soon", "not_started", "2026-10-29", "2026-10-30", "UTC", "2026-10-29T00:01:00Z", "", false},
		{"started already", "task_start_soon", "in_progress", "2026-11-02", "2026-11-03", "UTC", "2026-10-29T00:01:00Z", "", false},
		{"completed", "task_overdue", "completed", "2026-10-01", "2026-10-28", "UTC", "2026-10-29T00:01:00Z", "", true},
		{"DST offset", "task_due_soon", "in_progress", "2026-10-01", "2026-11-02", "America/New_York", "2026-10-29T00:01:00Z", "2026-10-30T13:00:00Z", false},
		{"year boundary", "task_due_soon", "in_progress", "2026-12-01", "2027-01-04", "UTC", "2026-12-29T00:01:00Z", "2027-01-01T09:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *time.Time
			err := pool.QueryRow(context.Background(), `SELECT reminder_schedule($1,$2,$3::date,$4::date,$5,$6::timestamptz,$7)`, tc.kind, tc.status, tc.start, tc.end, tc.tz, tc.now, tc.immediate).Scan(&got)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal(*got)
				}
				return
			}
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || !got.Equal(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

// Inspection does not generate work: writes must already have persisted plans.
type reminderInspection struct{ Generated, SkippedRecipients int }

func inspectReminderPlans(h *Harness) (reminderInspection, error) {
	var r reminderInspection
	err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders WHERE status='pending'`).Scan(&r.Generated)
	if err != nil {
		return r, err
	}
	err = h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE NOT reminder_enabled OR status<>'active' OR email=''`).Scan(&r.SkippedRecipients)
	return r, err
}
