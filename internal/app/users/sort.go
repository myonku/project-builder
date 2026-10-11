package users

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// 用户列表端点的排序白名单（POST /users/query）。
//
// 每个条目都是硬编码的"客户端字段名 → 数据库列名"映射：
// 列名永不来自用户输入，这是 SQL 注入的第一道防线，
// 同时保证"未知排序字段返回 400"而不是被静默丢弃（早期实现的缺陷）。
//
// 列名不带表别名是刻意的：该查询只 FROM users，没有 JOIN，
// 因此裸 id 不会被判为 ambiguous。若日后在这里加入 JOIN，
// 必须同时给全部列加上别名，否则会立刻复现 42702（ORDER BY "id" is ambiguous）。
var UserSort = listquery.NewSortSpec([]listquery.SortField{
	{Name: "username", Column: "username"},
	{Name: "display_name", Column: "display_name"},
	{Name: "email", Column: "email"},
	{Name: "status", Column: "status"},
	{Name: "system_role", Column: "system_role"},
	{Name: "created_at", Column: "created_at"},
	{Name: "updated_at", Column: "updated_at"},
	{Name: "last_login_at", Column: "last_login_at"},
}, "created_at", true, "id")
