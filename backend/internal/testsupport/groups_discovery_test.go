package testsupport

import (
	"context"
	"strings"
	"testing"
)

func TestGroupDiscoveryScopeAndSearch(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "discover_owner")
	applicant := h.RegisterAndLogin(t, "discover_applicant")
	group := h.CreateGroup(t, owner, "工程 Alpha_100%")
	h.CreateGroup(t, owner, "工程 Beta")
	disabled := h.CreateGroup(t, owner, "停用工程")
	_, err := h.Pool.Exec(context.Background(), `UPDATE groups SET status='disabled' WHERE id=$1`, disabled)
	if err != nil {
		t.Fatal(err)
	}
	query := func(body map[string]any) Response {
		return applicant.Client.Post(URL("/groups/discover/query"), body).RequireStatus(t, 200)
	}
	for _, q := range []string{"ALPHA_100%", strings.ToUpper(group), " 工程 Alpha_100% "} {
		rows := query(map[string]any{"q": q}).ListData(t)
		if len(rows) != 1 || Str(rows[0], "id") != group {
			t.Fatalf("搜索失败 %q: %v", q, rows)
		}
		if rows[0]["membership_status"] != nil || rows[0]["request_status"] != nil || rows[0]["can_review"] != false {
			t.Fatalf("本人投影错误: %v", rows)
		}
		for _, field := range []string{"owner_id", "members", "member_count", "email"} {
			if _, ok := rows[0][field]; ok {
				t.Fatalf("不应公开字段 %s", field)
			}
		}
	}
	if len(query(map[string]any{"q": "%"}).ListData(t)) != 1 || len(query(map[string]any{"q": "_"}).ListData(t)) != 1 || len(query(map[string]any{"q": `\`}).ListData(t)) != 0 {
		t.Fatal("搜索必须是字面子串")
	}
	first := query(map[string]any{"page_size": 1, "sort": "name"})
	second := query(map[string]any{"page_size": 1, "page": 2, "sort": "name"})
	if Int(first.Pagination(t), "total") != 2 || Str(first.ListData(t)[0], "id") == Str(second.ListData(t)[0], "id") {
		t.Fatal("分页或停用过滤失败")
	}
	if len(query(map[string]any{"page": 3, "page_size": 1}).ListData(t)) != 0 {
		t.Fatal("越界页必须为空")
	}
	ownerRows := owner.Client.Post(URL("/groups/discover/query"), map[string]any{"q": group}).RequireStatus(t, 200).ListData(t)
	if ownerRows[0]["can_review"] != true || Str(ownerRows[0], "membership_status") != "active" {
		t.Fatal("所有者投影错误")
	}
	applicant.Client.Get(URL("/groups/"+group)).RequireStatus(t, 404)
	applicant.Client.Post(URL("/groups/"+group+"/members/query"), map[string]any{}).RequireStatus(t, 404)
	applicant.Client.Post(URL("/groups/"+group+"/join-requests/query"), map[string]any{}).RequireStatus(t, 404)
	for _, body := range []map[string]any{{"q": strings.Repeat("字", 161)}, {"page": 0}, {"page_size": 101}, {"sort": "owner_id"}} {
		applicant.Client.Post(URL("/groups/discover/query"), body).RequireStatus(t, 400)
	}
	applicant.Client.Post(URL("/groups/discover/query"), map[string]any{"user_id": owner.UserID}).RequireStatus(t, 422)
	h.Client(t).Post(URL("/groups/discover/query"), map[string]any{}).RequireStatus(t, 401)
}

func TestMyGroupJoinRequestsLifecycle(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "join_owner")
	applicant := h.RegisterAndLogin(t, "join_applicant")
	other := h.RegisterAndLogin(t, "join_other")
	group := h.CreateGroup(t, owner, "加入流程")
	otherGroup := h.CreateGroup(t, owner, "另一个组")
	apply := func(id string) {
		applicant.Client.Post(URL("/groups/"+id+"/join-requests"), map[string]any{}).RequireStatus(t, 201)
	}
	mine := func(body map[string]any) Response {
		return applicant.Client.Post(URL("/me/group-join-requests/query"), body).RequireStatus(t, 200)
	}
	if len(mine(map[string]any{}).ListData(t)) != 0 {
		t.Fatal("新用户申请应为空")
	}
	apply(group)
	other.Client.Post(URL("/groups/"+group+"/join-requests"), map[string]any{}).RequireStatus(t, 201)
	applicant.Client.Post(URL("/groups/"+group+"/join-requests"), nil).RequireStatus(t, 409)
	row := mine(map[string]any{"status": "pending"}).ListData(t)[0]
	if Str(row, "group_name") != "加入流程" || Str(row, "status") != "pending" || row["reviewed_at"] != nil {
		t.Fatalf("申请记录错误: %v", row)
	}
	originalID := Str(row, "id")
	discovery := applicant.Client.Post(URL("/groups/discover/query"), map[string]any{"q": group}).RequireStatus(t, 200).ListData(t)[0]
	if Str(discovery, "request_status") != "pending" {
		t.Fatal("搜索没有显示待审核")
	}
	review := func(decision string) {
		owner.Client.Post(URL("/groups/"+group+"/join-requests/"+applicant.UserID+"/review"), map[string]any{"status": decision}).RequireStatus(t, 200)
	}
	review("rejected")
	if len(mine(map[string]any{"status": "pending"}).ListData(t)) != 0 {
		t.Fatal("拒绝后 pending 筛选错误")
	}
	apply(group)
	row = mine(map[string]any{}).ListData(t)[0]
	if Str(row, "id") != originalID || Str(row, "status") != "pending" || row["reviewed_at"] != nil {
		t.Fatal("重新申请应更新同一记录")
	}
	review("approved")
	row = mine(map[string]any{}).ListData(t)[0]
	if Str(row, "membership_status") != "active" || Str(row, "status") != "approved" || row["reviewed_at"] == nil {
		t.Fatal("通过后的本人状态错误")
	}
	applicant.Client.Get(URL("/groups/"+group)).RequireStatus(t, 200)
	owner.Client.Delete(URL("/groups/"+group+"/members/"+applicant.UserID), nil).RequireStatus(t, 204)
	apply(group)
	apply(otherGroup)
	for _, id := range []string{group, otherGroup} {
		if _, err := h.Pool.Exec(context.Background(), `UPDATE group_join_requests SET updated_at='2026-10-26T00:00:00Z' WHERE group_id=$1 AND user_id=$2`, id, applicant.UserID); err != nil {
			t.Fatal(err)
		}
	}
	first := mine(map[string]any{"page_size": 1})
	second := mine(map[string]any{"page_size": 1, "page": 2})
	if Int(first.Pagination(t), "total") != 2 || Str(first.ListData(t)[0], "id") == Str(second.ListData(t)[0], "id") {
		t.Fatal("稳定分页/本人范围错误")
	}
	promoteToAdmin(t, h, applicant.UserID)
	if Int(mine(map[string]any{}).Pagination(t), "total") != 2 {
		t.Fatal("管理员不应扩大本人范围")
	}
	if _, err := h.Pool.Exec(context.Background(), `UPDATE groups SET status='disabled' WHERE id=$1`, group); err != nil {
		t.Fatal(err)
	}
	rows := mine(map[string]any{}).ListData(t)
	for _, r := range rows {
		if Str(r, "group_id") == group && (Str(r, "group_status") != "disabled" || Str(r, "membership_status") != "removed") {
			t.Fatalf("停用/移除状态丢失: %v", r)
		}
	}
	applicant.Client.Post(URL("/groups/"+group+"/join-requests"), nil).RequireStatus(t, 409)
	for _, body := range []map[string]any{{"status": "bad"}, {"page": 0}, {"page_size": 101}, {"sort": "group_name"}} {
		applicant.Client.Post(URL("/me/group-join-requests/query"), body).RequireStatus(t, 400)
	}
	applicant.Client.Post(URL("/me/group-join-requests/query"), map[string]any{"user_id": other.UserID}).RequireStatus(t, 422)
	h.Client(t).Post(URL("/me/group-join-requests/query"), map[string]any{}).RequireStatus(t, 401)
}
