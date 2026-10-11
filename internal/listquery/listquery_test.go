package listquery

import (
	"strings"
	"testing"

	"github.com/myonku/project-builder/internal/domain"
)

// intPtr 构造分页参数指针。
func intPtr(value int) *int { return &value }

func TestResolvePageDefaults(t *testing.T) {
	t.Parallel()

	pagination, err := ResolvePage(nil, nil)
	if err != nil {
		t.Fatalf("ResolvePage 返回错误: %v", err)
	}
	if pagination.Page != DefaultPage {
		t.Errorf("page = %d，期望默认 %d", pagination.Page, DefaultPage)
	}
	if pagination.PageSize != DefaultPageSize {
		t.Errorf("page_size = %d，期望默认 %d", pagination.PageSize, DefaultPageSize)
	}
}

func TestResolvePageComputesLimitOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page, pageSize        int
		wantLimit, wantOffset int
	}{
		{page: 1, pageSize: 20, wantLimit: 20, wantOffset: 0},
		{page: 2, pageSize: 20, wantLimit: 20, wantOffset: 20},
		{page: 5, pageSize: 100, wantLimit: 100, wantOffset: 400},
	}

	for _, tc := range tests {
		pagination, err := ResolvePage(intPtr(tc.page), intPtr(tc.pageSize))
		if err != nil {
			t.Fatalf("ResolvePage(%d, %d) 返回错误: %v", tc.page, tc.pageSize, err)
		}
		if got := pagination.Limit(); got != tc.wantLimit {
			t.Errorf("page=%d size=%d Limit() = %d，期望 %d", tc.page, tc.pageSize, got, tc.wantLimit)
		}
		if got := pagination.Offset(); got != tc.wantOffset {
			t.Errorf("page=%d size=%d Offset() = %d，期望 %d", tc.page, tc.pageSize, got, tc.wantOffset)
		}
	}
}

// TestResolvePageRejectsOutOfRange 是本包最重要的用例之一。
//
// 早期实现实测：page_size=500 被接受并原样回显，page=0 返回 200。
// 服务端必须显式拒绝，而不是静默夹取——静默纠正会让客户端
// 以为自己设置的参数生效了。
func TestResolvePageRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		page     *int
		pageSize *int
		wantCode string
	}{
		{name: "page 为 0", page: intPtr(0), wantCode: "page"},
		{name: "page 为负", page: intPtr(-1), wantCode: "page"},
		{name: "page_size 为 0", pageSize: intPtr(0), wantCode: "page_size"},
		{name: "page_size 为负", pageSize: intPtr(-5), wantCode: "page_size"},
		{name: "page_size 超过上限", pageSize: intPtr(MaxPageSize + 1), wantCode: "page_size"},
		{name: "page_size 远超上限", pageSize: intPtr(500), wantCode: "page_size"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolvePage(tc.page, tc.pageSize)
			if err == nil {
				t.Fatal("期望拒绝非法分页，实际通过")
			}
			var domainErr *domain.Error
			if !domain.AsErrorAs(err, &domainErr) {
				t.Fatalf("期望领域错误，实际 %T", err)
			}
			if domainErr.Code != domain.CodeInvalidRequest {
				t.Errorf("错误码 = %q，期望 %q", domainErr.Code, domain.CodeInvalidRequest)
			}
			if _, ok := domainErr.Fields[tc.wantCode]; !ok {
				t.Errorf("字段明细缺少 %q，实际 %v", tc.wantCode, domainErr.Fields)
			}
		})
	}
}

func TestResolvePageAcceptsBoundaries(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, MaxPageSize} {
		if _, err := ResolvePage(intPtr(1), intPtr(size)); err != nil {
			t.Errorf("page_size=%d 应被接受，实际: %v", size, err)
		}
	}
}

// newSpec 构造一个测试用排序白名单。
func newSpec() *SortSpec {
	return NewSortSpec([]SortField{
		{Name: "name", Column: "x.name"},
		{Name: "end_date", Column: "x.end_date"},
		{Name: "created_at", Column: "x.created_at"},
	}, "created_at", true, "x.id")
}

func TestSortSpecDefaults(t *testing.T) {
	t.Parallel()

	spec := newSpec()

	for _, raw := range []string{"", "   "} {
		resolved, err := spec.Resolve(raw)
		if err != nil {
			t.Fatalf("Resolve(%q) 返回错误: %v", raw, err)
		}
		if len(resolved) != 1 {
			t.Fatalf("Resolve(%q) 项数 = %d，期望 1", raw, len(resolved))
		}
		if resolved[0].Field.Column != "x.created_at" || !resolved[0].Desc {
			t.Errorf("Resolve(%q) = %+v，期望默认 created_at 降序", raw, resolved[0])
		}
	}
}

func TestSortSpecResolveDirections(t *testing.T) {
	t.Parallel()

	spec := newSpec()

	tests := []struct {
		raw      string
		wantCols []string
		wantDesc []bool
	}{
		{raw: "name", wantCols: []string{"x.name"}, wantDesc: []bool{false}},
		{raw: "-name", wantCols: []string{"x.name"}, wantDesc: []bool{true}},
		{raw: "name,-end_date", wantCols: []string{"x.name", "x.end_date"}, wantDesc: []bool{false, true}},
		{raw: " name , -end_date ", wantCols: []string{"x.name", "x.end_date"}, wantDesc: []bool{false, true}},
	}

	for _, tc := range tests {
		resolved, err := spec.Resolve(tc.raw)
		if err != nil {
			t.Fatalf("Resolve(%q) 返回错误: %v", tc.raw, err)
		}
		if len(resolved) != len(tc.wantCols) {
			t.Fatalf("Resolve(%q) 项数 = %d，期望 %d", tc.raw, len(resolved), len(tc.wantCols))
		}
		for i := range resolved {
			if resolved[i].Field.Column != tc.wantCols[i] {
				t.Errorf("Resolve(%q)[%d] 列 = %q，期望 %q", tc.raw, i, resolved[i].Field.Column, tc.wantCols[i])
			}
			if resolved[i].Desc != tc.wantDesc[i] {
				t.Errorf("Resolve(%q)[%d] 降序 = %v，期望 %v", tc.raw, i, resolved[i].Desc, tc.wantDesc[i])
			}
		}
	}
}

// TestSortSpecRejectsUnknownField 覆盖"未知排序字段返回 400"。
//
// 早期实现实测 sort 校验后被丢弃、SQL 硬编码 ORDER BY。
func TestSortSpecRejectsUnknownField(t *testing.T) {
	t.Parallel()

	spec := newSpec()

	for _, raw := range []string{"nonsense", "-nonsense", "name.nope", "1;DROP TABLE users"} {
		_, err := spec.Resolve(raw)
		if err == nil {
			t.Fatalf("Resolve(%q) 应被拒绝", raw)
		}
		var domainErr *domain.Error
		if !domain.AsErrorAs(err, &domainErr) {
			t.Fatalf("期望领域错误，实际 %T", err)
		}
		if domainErr.Code != domain.CodeInvalidRequest {
			t.Errorf("错误码 = %q，期望 %q", domainErr.Code, domain.CodeInvalidRequest)
		}
		if _, ok := domainErr.Fields["sort"]; !ok {
			t.Errorf("字段明细缺少 sort，实际 %v", domainErr.Fields)
		}
	}
}

func TestSortSpecRejectsDuplicateField(t *testing.T) {
	t.Parallel()

	if _, err := newSpec().Resolve("name,name"); err == nil {
		t.Fatal("重复排序字段应被拒绝（静默去重会掩盖客户端错误）")
	}
}

// TestOrderByAlwaysAppendsQualifiedID 覆盖次级排序键的两个关键性质。
func TestOrderByAlwaysAppendsQualifiedID(t *testing.T) {
	t.Parallel()

	spec := newSpec()
	resolved, err := spec.Resolve("name")
	if err != nil {
		t.Fatalf("Resolve 返回错误: %v", err)
	}

	clause := spec.OrderBy(resolved)

	// 性质一：必须存在次级键。
	if !strings.Contains(clause, "x.id ASC") {
		t.Fatalf("ORDER BY 必须追加 id 次级键，实际 %q", clause)
	}
	// 性质二：次级键必须是**限定**列名。
	//
	// 列表查询普遍带 JOIN，多张表都有 id 列；裸 "id" 会被 PostgreSQL
	// 判为 'ORDER BY "id" is ambiguous'（实测 42702）。
	if strings.Contains(clause, ", id ") || clause == "id ASC" {
		t.Fatalf("ORDER BY 的次级键必须是限定列名（如 x.id），实际 %q", clause)
	}
}

func TestOrderByDoesNotDuplicateID(t *testing.T) {
	t.Parallel()

	spec := NewSortSpec([]SortField{
		{Name: "id", Column: "x.id"},
		{Name: "name", Column: "x.name"},
	}, "id", false, "x.id")

	resolved, err := spec.Resolve("id")
	if err != nil {
		t.Fatalf("Resolve 返回错误: %v", err)
	}

	clause := spec.OrderBy(resolved)
	if strings.Count(clause, "x.id") != 1 {
		t.Fatalf("显式指定 id 时不应重复追加，实际 %q", clause)
	}
}

func TestOrderByMultipleFields(t *testing.T) {
	t.Parallel()

	spec := newSpec()
	resolved, err := spec.Resolve("-end_date,name")
	if err != nil {
		t.Fatalf("Resolve 返回错误: %v", err)
	}

	want := "x.end_date DESC, x.name ASC, x.id ASC"
	if got := spec.OrderBy(resolved); got != want {
		t.Fatalf("OrderBy = %q，期望 %q", got, want)
	}
}

func TestAllowedReturnsSortedNames(t *testing.T) {
	t.Parallel()

	allowed := newSpec().Allowed()
	want := []string{"created_at", "end_date", "name"}
	if len(allowed) != len(want) {
		t.Fatalf("Allowed() = %v，期望 %v", allowed, want)
	}
	for i := range want {
		if allowed[i] != want[i] {
			t.Fatalf("Allowed() = %v，期望 %v", allowed, want)
		}
	}
}

// TestNewSortSpecPanicsOnMisconfiguration 覆盖编程错误的快速失败。
func TestNewSortSpecPanicsOnMisconfiguration(t *testing.T) {
	t.Parallel()

	fields := []SortField{{Name: "name", Column: "x.name"}}

	t.Run("默认字段不在白名单", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("默认排序字段不在白名单时必须 panic")
			}
		}()
		NewSortSpec(fields, "missing", false, "x.id")
	})

	t.Run("缺少限定 id 列", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("未提供限定 id 列时必须 panic（否则 JOIN 查询会报 ambiguous）")
			}
		}()
		NewSortSpec(fields, "name", false, "")
	})
}

func TestPageRequestPagination(t *testing.T) {
	t.Parallel()

	page, size := 3, 10
	request := PageRequest{Page: &page, PageSize: &size}

	pagination, err := request.Pagination()
	if err != nil {
		t.Fatalf("Pagination 返回错误: %v", err)
	}
	if pagination.Page != 3 || pagination.PageSize != 10 {
		t.Fatalf("Pagination = %+v，期望 page=3 size=10", pagination)
	}

	// 零值（客户端完全省略）应使用默认值。
	empty := PageRequest{}
	if _, err := empty.Pagination(); err != nil {
		t.Fatalf("空 PageRequest 应使用默认值，实际: %v", err)
	}
}
