package attachments

import (
	"github.com/myonku/project-builder/internal/listquery"
)

// AttachmentSort 是附件列表端点的排序白名单。
//
// 默认按上传时间**倒序**（最近上传的排前面），并由 listquery 自动追加 a.id
// 作为次级键，保证翻页不重不漏。
var AttachmentSort = listquery.NewSortSpec([]listquery.SortField{
	{Name: "created_at", Column: "a.created_at"},
	{Name: "updated_at", Column: "a.updated_at"},
	{Name: "original_name", Column: "a.original_name"},
	{Name: "size_bytes", Column: "a.size_bytes"},
	{Name: "extension", Column: "a.extension"},
}, "created_at", true, "a.id")
