package groups

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// 各列表端点的排序白名单。
//
// 每个条目都是硬编码的"客户端字段名 → 数据库列名"映射：
// 列名永不来自用户输入，这是 SQL 注入的第一道防线，
// 同时保证"未知排序字段返回 400"而不是被静默丢弃（Python 版本的缺陷）。
var (
	// GroupSort 用于 POST /groups/query。
	GroupSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "name", Column: "g.name"},
		{Name: "status", Column: "g.status"},
		{Name: "created_at", Column: "g.created_at"},
		{Name: "updated_at", Column: "g.updated_at"},
	}, "created_at", true, "g.id")

	// MemberSort 用于 POST /groups/{id}/members/query。
	MemberSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "username", Column: "u.username"},
		{Name: "display_name", Column: "u.display_name"},
		{Name: "role", Column: "m.role"},
		{Name: "status", Column: "m.status"},
		{Name: "joined_at", Column: "m.joined_at"},
	}, "joined_at", false, "m.id")

	// JoinRequestSort 用于 POST /groups/{id}/join-requests/query。
	JoinRequestSort = listquery.NewSortSpec([]listquery.SortField{
		{Name: "status", Column: "r.status"},
		{Name: "created_at", Column: "r.created_at"},
		{Name: "updated_at", Column: "r.updated_at"},
		{Name: "username", Column: "u.username"},
	}, "created_at", false, "r.id")
)
