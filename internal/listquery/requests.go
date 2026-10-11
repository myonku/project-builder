package listquery

// PageRequest 是可嵌入任意查询请求体的分页字段。
//
// 使用指针以区分"客户端未提供"（nil，取默认值）与"显式提供 0"
// （后者必须被拒绝，见 ResolvePage）。JSON 中省略即 nil。
type PageRequest struct {
	Page     *int `json:"page,omitempty"`
	PageSize *int `json:"page_size,omitempty"`
}

// Pagination 解析分页参数。
func (r PageRequest) Pagination() (Pagination, error) {
	return ResolvePage(r.Page, r.PageSize)
}

// SortRequest 是可嵌入任意查询请求体的排序字段。
type SortRequest struct {
	// Sort 形如 "-end_date,id"：前缀 - 表示降序，逗号分隔多字段。
	Sort string `json:"sort,omitempty"`
}
