package tasks

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// 任务列表端点的排序白名单。
//
// 默认按开始日期升序（接口契约 3.3 与甘特图展示习惯），
// 并由 listquery.OrderBy 自动追加 id 作为次级键，保证翻页不重不漏。
var (
	// TaskSort 用于 POST /projects/{project_id}/tasks/query。
	TaskSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "name", Column: "t.name"},
		{Name: "status", Column: "t.status"},
		{Name: "progress", Column: "t.progress"},
		{Name: "priority", Column: "t.priority"},
		{Name: "start_date", Column: "t.start_date"},
		{Name: "end_date", Column: "t.end_date"},
		{Name: "created_at", Column: "t.created_at"},
		{Name: "updated_at", Column: "t.updated_at"},
	}, "start_date", false, "t.id")

	// MemberSort 用于 POST /tasks/{id}/members/query。
	MemberSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "username", Column: "u.username"},
		{Name: "display_name", Column: "u.display_name"},
		{Name: "role", Column: "tm.role"},
		{Name: "created_at", Column: "tm.created_at"},
	}, "created_at", false, "tm.id")
)
