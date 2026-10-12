package audit

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// AuditSort 是审计列表端点的排序白名单。
//
// 默认按**时间倒序**（最近发生的最先看到），并由 listquery 自动追加
// a.id 作为次级键，保证翻页不重不漏（同一微秒内的多条记录也有稳定顺序）。
var AuditSort = listquery.NewSortSpec([]listquery.SortField{
	{Name: "created_at", Column: "a.created_at"},
	{Name: "action", Column: "a.action"},
	{Name: "object_type", Column: "a.object_type"},
}, "created_at", true, "a.id")
