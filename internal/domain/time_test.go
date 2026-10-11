package domain

import (
	"errors"
	"testing"
)

func TestValidateDateRange(t *testing.T) {
	t.Parallel()

	start := mustDate(t, "2026-10-01")

	tests := []struct {
		name    string
		end     string
		wantErr bool
	}{
		{name: "同一天合法", end: "2026-10-01"},
		{name: "结束晚于开始合法", end: "2026-10-31"},
		{name: "结束早于开始非法", end: "2026-09-30", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDateRange(start, mustDate(t, tc.end))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际为 nil")
				}
				var domainErr *Error
				if !errors.As(err, &domainErr) {
					t.Fatalf("期望 *domain.Error，实际为 %T", err)
				}
				if domainErr.Code != CodeInvalidDateRange {
					t.Fatalf("错误码 = %q，期望 %q", domainErr.Code, CodeInvalidDateRange)
				}
				// 字段级明细必须同时指出两端，便于前端定位。
				for _, key := range []string{"start_date", "end_date"} {
					if _, ok := domainErr.Fields[key]; !ok {
						t.Errorf("缺少字段明细 %q", key)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("期望无错误，实际为 %v", err)
			}
		})
	}
}

func TestToday(t *testing.T) {
	t.Parallel()

	// 2026-10-01T20:00:00Z 在 UTC 是 10-01，在 UTC+8 已经是 10-02。
	now := mustTimestamp(t, "2026-10-01T20:00:00Z")

	tests := []struct {
		timezone string
		want     string
	}{
		{timezone: "UTC", want: "2026-10-01"},
		{timezone: "Asia/Shanghai", want: "2026-10-02"},
		{timezone: "America/New_York", want: "2026-10-01"},
		{timezone: "", want: "2026-10-01"},
	}

	for _, tc := range tests {
		t.Run(tc.timezone, func(t *testing.T) {
			t.Parallel()
			got, err := Today(now, tc.timezone)
			if err != nil {
				t.Fatalf("Today 返回错误: %v", err)
			}
			if formatted := FormatDate(got); formatted != tc.want {
				t.Fatalf("Today(%q) = %s，期望 %s", tc.timezone, formatted, tc.want)
			}
		})
	}
}

func TestTodayRejectsInvalidTimezone(t *testing.T) {
	t.Parallel()
	if _, err := Today(mustTimestamp(t, "2026-10-01T00:00:00Z"), "Not/AZone"); err == nil {
		t.Fatal("期望无效时区返回错误，实际为 nil")
	}
}

func TestIsOverdue(t *testing.T) {
	t.Parallel()

	today := mustDate(t, "2026-10-10")

	tests := []struct {
		name   string
		end    string
		status string
		want   bool
	}{
		{name: "未开始且已过期", end: "2026-10-09", status: TaskStatusNotStarted, want: true},
		{name: "进行中且已过期", end: "2026-10-09", status: TaskStatusInProgress, want: true},
		{name: "今天到期不算逾期", end: "2026-10-10", status: TaskStatusInProgress, want: false},
		{name: "未来到期不算逾期", end: "2026-10-11", status: TaskStatusNotStarted, want: false},
		// 已完成 / 已取消是终态，无论日期如何都不算逾期。
		{name: "已完成不算逾期", end: "2026-10-01", status: TaskStatusCompleted, want: false},
		{name: "已取消不算逾期", end: "2026-10-01", status: TaskStatusCancelled, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsOverdue(mustDate(t, tc.end), tc.status, today)
			if got != tc.want {
				t.Fatalf("IsOverdue(%s, %s, %s) = %v，期望 %v",
					tc.end, tc.status, FormatDate(today), got, tc.want)
			}
		})
	}
}

func TestProgressStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		progress int
		want     string
	}{
		{progress: 0, want: TaskStatusNotStarted},
		{progress: 1, want: TaskStatusInProgress},
		{progress: 50, want: TaskStatusInProgress},
		{progress: 99, want: TaskStatusInProgress},
		{progress: 100, want: TaskStatusCompleted},
	}

	for _, tc := range tests {
		if got := ProgressStatus(tc.progress); got != tc.want {
			t.Errorf("ProgressStatus(%d) = %q，期望 %q", tc.progress, got, tc.want)
		}
	}
}

func TestValidateProgress(t *testing.T) {
	t.Parallel()

	for _, progress := range []int{-1, 101} {
		if err := ValidateProgress(progress); err == nil {
			t.Errorf("ValidateProgress(%d) 期望错误，实际为 nil", progress)
		}
	}
	for _, progress := range []int{0, 1, 99, 100} {
		if err := ValidateProgress(progress); err != nil {
			t.Errorf("ValidateProgress(%d) 期望无错误，实际为 %v", progress, err)
		}
	}
}
