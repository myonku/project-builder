package reminders

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// ReminderSort 是提醒列表端点的排序白名单。
//
// 默认按计划发送时间**倒序**（最近要发的排前面），并由 listquery 自动追加
// r.id 作为次级键，保证翻页不重不漏。
var ReminderSort = listquery.NewSortSpec([]listquery.SortField{
	{Name: "scheduled_for", Column: "r.scheduled_for"},
	{Name: "created_at", Column: "r.created_at"},
	{Name: "updated_at", Column: "r.updated_at"},
	{Name: "kind", Column: "r.kind"},
	{Name: "status", Column: "r.status"},
	{Name: "attempts", Column: "r.attempts"},
}, "scheduled_for", true, "r.id")
