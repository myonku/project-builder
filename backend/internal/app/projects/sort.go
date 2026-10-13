package projects

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// 项目列表端点的排序白名单。
//
// 列名硬编码，永不来自用户输入。默认按结束日期升序（接口契约 4.5 的
// "结束日期排序"），并由 listquery.OrderBy 自动追加 id 作为次级键。
var (
	// ProjectSort 用于 POST /projects/query。
	ProjectSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "name", Column: "p.name"},
		{Name: "status", Column: "p.status"},
		{Name: "visibility", Column: "p.visibility"},
		{Name: "start_date", Column: "p.start_date"},
		{Name: "end_date", Column: "p.end_date"},
		{Name: "created_at", Column: "p.created_at"},
		{Name: "updated_at", Column: "p.updated_at"},
	}, "end_date", false, "p.id")

	// MemberSort 用于 POST /projects/{id}/members/query。
	MemberSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "username", Column: "u.username"},
		{Name: "display_name", Column: "u.display_name"},
		{Name: "role", Column: "pm.role"},
		{Name: "created_at", Column: "pm.created_at"},
	}, "created_at", false, "pm.id")
)
