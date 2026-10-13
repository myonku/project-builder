// Package listquery 集中实现列表接口的分页与排序规则。
//
// 为什么必须集中：早期实现在这两处各犯过一次错——
//   - page_size=500 被原样接受并回显，page=0 返回 200；
//   - sort 校验后被丢弃，SQL 硬编码 ORDER BY start_date, id，且没有分页下推。
//
// 因此本文档要求的规则必须只有一处实现，并被所有列表端点复用。
//
// 规则（docs/S02-HTTP接口契约.md 2.5）：
//   - page 从 1 开始，默认 1；page_size 默认 20，最大 100；
//   - 非法分页由服务端拒绝（400 invalid_request），不得静默纠正；
//   - 排序字段必须白名单校验，未知字段返回 400；
//   - 排序必须追加 id 作为次级键，否则等值键在翻页时可能重复或漏出；
//   - 分页必须在 SQL 层 LIMIT/OFFSET，不得全量载入内存后切片。
package listquery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/myonku/project-builder/internal/domain"
)

// 分页边界。与接口契约 2.5 一致。
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Pagination 是分页参数。
type Pagination struct {
	Page     int
	PageSize int
}

// Offset 返回 SQL OFFSET。
func (p Pagination) Offset() int { return (p.Page - 1) * p.PageSize }

// Limit 返回 SQL LIMIT。
func (p Pagination) Limit() int { return p.PageSize }

// ResolvePage 把可选的请求参数解析为合法的分页参数。
//
// 指针为 nil 表示客户端未提供该字段，使用默认值。
// 一旦提供，就必须在合法范围内，否则返回 400（domain.ErrInvalidRequest）并带字段明细。
// 这里刻意不做"静默夹取"（例如把 page_size=500 改成 100）：
// 静默纠正会让客户端以为自己设置的参数生效了。
func ResolvePage(page, pageSize *int) (Pagination, error) {
	resolved := Pagination{Page: DefaultPage, PageSize: DefaultPageSize}
	fields := map[string]string{}

	if page != nil {
		resolved.Page = *page
		if resolved.Page < DefaultPage {
			fields["page"] = fmt.Sprintf("页码必须不小于 %d", DefaultPage)
		}
	}
	if pageSize != nil {
		resolved.PageSize = *pageSize
		if resolved.PageSize < 1 {
			fields["page_size"] = "每页数量必须为正整数"
		} else if resolved.PageSize > MaxPageSize {
			fields["page_size"] = fmt.Sprintf("每页数量不得超过 %d", MaxPageSize)
		}
	}

	if len(fields) > 0 {
		return Pagination{}, domain.ErrInvalidRequest.WithFields(fields)
	}
	return resolved, nil
}

// SortField 描述一个可排序字段。
type SortField struct {
	// Name 是客户端在请求体中使用的字段名（snake_case）。
	Name string
	// Column 是数据库中对应的列名；必须是硬编码常量，绝不拼接用户输入。
	Column string
}

// SortValue 是解析后的排序项。
type SortValue struct {
	Field SortField
	Desc  bool
}

// SortSpec 是某个列表端点允许的排序白名单。
type SortSpec struct {
	fields      map[string]SortField
	order       []string
	defaultSort SortValue
	// idColumn 是次级排序键的**限定列名**（如 g.id）。
	//
	// 必须是限定名：列表查询普遍带 JOIN，而多张表都有 id 列，
	// 写成裸 "id" 会被 PostgreSQL 判为 'ORDER BY "id" is ambiguous'（实测 42702）。
	// 因此次级键必须与调用方使用的表别名一致，构造时显式传入。
	idColumn string
}

// NewSortSpec 构造排序白名单。
//
// idColumn 是次级排序键的限定列名（例如 "g.id"），必须与查询中使用的别名一致。
// defaultField 必须包含在 fields 中，否则 panic（编程错误）。
func NewSortSpec(fields []SortField, defaultField string, defaultDesc bool, idColumn string) *SortSpec {
	if idColumn == "" {
		panic("listquery: 必须提供次级排序键的限定列名（如 g.id），否则 JOIN 查询会报 ambiguous")
	}
	spec := &SortSpec{
		fields:   make(map[string]SortField, len(fields)),
		idColumn: idColumn,
	}
	for _, field := range fields {
		spec.fields[field.Name] = field
		spec.order = append(spec.order, field.Name)
	}
	if _, ok := spec.fields[defaultField]; !ok {
		panic("listquery: 默认排序字段不在白名单中: " + defaultField)
	}
	spec.defaultSort = SortValue{Field: spec.fields[defaultField], Desc: defaultDesc}
	return spec
}

// Allowed 返回白名单中的字段名（已排序），便于文档与错误信息展示。
func (s *SortSpec) Allowed() []string {
	out := append([]string(nil), s.order...)
	sort.Strings(out)
	return out
}

// Resolve 解析请求中的 sort 字符串。
//
// 语法：`field` 或 `-field`（前缀 - 表示降序），多个字段用逗号分隔，例如 `-end_date,id`。
// 未提供时使用默认排序。任一字段不在白名单内即返回 400。
func (s *SortSpec) Resolve(raw string) ([]SortValue, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []SortValue{s.defaultSort}, nil
	}

	var resolved []SortValue
	fields := map[string]string{}
	seen := map[string]bool{}

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		desc := false
		if strings.HasPrefix(part, "-") {
			desc = true
			part = strings.TrimSpace(part[1:])
		}
		if part == "" {
			continue
		}

		field, ok := s.fields[part]
		if !ok {
			fields["sort"] = fmt.Sprintf("不支持的排序字段 %q，可用字段：%s",
				part, strings.Join(s.Allowed(), ", "))
			continue
		}
		if seen[field.Name] {
			// 同一字段重复出现是客户端错误，静默去重会掩盖问题。
			fields["sort"] = fmt.Sprintf("排序字段 %q 重复", field.Name)
			continue
		}
		seen[field.Name] = true
		resolved = append(resolved, SortValue{Field: field, Desc: desc})
	}

	if len(fields) > 0 {
		return nil, domain.ErrInvalidRequest.WithFields(fields)
	}
	if len(resolved) == 0 {
		return []SortValue{s.defaultSort}, nil
	}
	return resolved, nil
}

// OrderBy 由已解析的排序项生成 ORDER BY 子句。
//
// 关键保证：始终追加**限定**的次级键（如 g.id ASC）。
//
// 为什么必须带限定名：列表查询普遍带 JOIN，而多张表都有 id 列。
// 写成裸 "id" 会被 PostgreSQL 判为 'ORDER BY "id" is ambiguous'（实测 42702 报错）。
//
// 为什么必须有次级键：没有它，等值键在分页时顺序不确定，
// 会出现"同一行在相邻两页重复出现"或"某行被跳过"。
func (s *SortSpec) OrderBy(values []SortValue) string {
	var parts []string
	hasID := false

	for _, value := range values {
		direction := "ASC"
		if value.Desc {
			direction = "DESC"
		}
		if value.Field.Column == s.idColumn {
			hasID = true
		}
		parts = append(parts, value.Field.Column+" "+direction)
	}

	if !hasID {
		parts = append(parts, s.idColumn+" ASC")
	}
	return strings.Join(parts, ", ")
}

// OrderBy 是 SortSpec.OrderBy 的包级便捷形式，供直接持有已解析排序项的场景使用。
//
// 保留该函数是为了让调用点保持 `listquery.OrderBy(order)` 的读法，
// 但它需要 spec 才能知道限定列名，因此签名要求显式传入。
func OrderBy(spec *SortSpec, values []SortValue) string {
	return spec.OrderBy(values)
}
