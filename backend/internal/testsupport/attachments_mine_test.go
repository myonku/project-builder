package testsupport

import (
	"net/http"
	"strings"
	"testing"
)

func TestMyAttachmentsQuery(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "mine_owner")
	other := h.RegisterAndLogin(t, "mine_other")
	group := h.CreateGroup(t, owner, "附件管理组")
	project := h.CreateProject(t, owner, group, "来源项目")
	task := h.CreateTask(t, owner, project, "来源任务", "2026-10-01", "2026-10-05")
	otherProject := h.CreateProject(t, other, h.CreateGroup(t, other, "其他组"), "他人项目")
	uploadAttachment(t, other.Client, "/projects/"+otherProject+"/attachments", "other.txt", "text/plain", []byte("other")).RequireStatus(t, 201)
	ids := []string{}
	for _, f := range []struct{ path, name string }{{"/projects/" + project, "Report.txt"}, {"/tasks/" + task, "report_100%.txt"}, {"/tasks/" + task, "another.txt"}} {
		r := uploadAttachment(t, owner.Client, f.path+"/attachments", f.name, "text/plain", []byte("test")).RequireStatus(t, 201)
		ids = append(ids, Str(r.Data(t), "id"))
	}
	query := func(body map[string]any) Response {
		return owner.Client.Post(URL("/me/attachments/query"), body).RequireStatus(t, 200)
	}
	promoteToAdmin(t, h, owner.UserID) // Administrative role must not broaden the uploader scope.
	all := query(map[string]any{"sort": "original_name", "page_size": 1})
	if Int(all.Pagination(t), "total") != 3 || len(all.ListData(t)) != 1 {
		t.Fatalf("分页/总数错误: %s", all.Body)
	}
	second := query(map[string]any{"sort": "original_name", "page_size": 1, "page": 2})
	if Str(all.ListData(t)[0], "id") == Str(second.ListData(t)[0], "id") {
		t.Fatal("分页重复")
	}
	row := query(map[string]any{"original_name": "REPORT_100%"}).ListData(t)
	if len(row) != 1 || Str(row[0], "id") != ids[1] {
		t.Fatalf("字面/大小写搜索错误: %v", row)
	}
	source := row[0]["source"].(map[string]any)
	if Str(source, "type") != "task" || Str(source["project"].(map[string]any), "id") != project || Str(source["task"].(map[string]any), "name") != "来源任务" {
		t.Fatalf("来源错误: %v", source)
	}
	if row[0]["project_id"] != nil {
		t.Fatal("任务附件的 project_id 应保留 null")
	}
	permissions := row[0]["permissions"].(map[string]any)
	if permissions["can_delete"] != true || permissions["can_purge"] != false {
		t.Fatalf("操作权限错误: %v", permissions)
	}
	if _, ok := row[0]["stored_key"]; ok {
		t.Fatal("泄漏存储 key")
	}
	if Int(query(map[string]any{"container_type": "project"}).Pagination(t), "total") != 1 {
		t.Fatal("类型筛选错误")
	}
	if Int(query(map[string]any{"project_id": project}).Pagination(t), "total") != 3 {
		t.Fatal("项目筛选未包含任务附件")
	}
	if Int(query(map[string]any{"project_id": otherProject}).Pagination(t), "total") != 0 {
		t.Fatal("泄漏他人项目附件")
	}
	owner.Client.Delete(URL("/attachments/"+ids[0]), nil).RequireStatus(t, 200)
	if Int(query(map[string]any{}).Pagination(t), "total") != 2 {
		t.Fatal("默认不应包含已删除")
	}
	deleted := query(map[string]any{"deleted_only": true}).ListData(t)
	if len(deleted) != 1 || deleted[0]["permissions"].(map[string]any)["can_purge"] != true {
		t.Fatal("回收站权限错误")
	}
	if Int(query(map[string]any{"include_deleted": true}).Pagination(t), "total") != 3 {
		t.Fatal("含已删除总数错误")
	}
	owner.Client.Post(URL("/attachments/"+ids[0]+"/purge"), nil).RequireStatus(t, 204)
	if Int(query(map[string]any{"include_deleted": true}).Pagination(t), "total") != 2 {
		t.Fatal("彻底删除未从管理列表消失")
	}
	for _, test := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{"uploader_id": other.UserID}, 422},
		{map[string]any{"project_id": "invalid"}, 422},
		{map[string]any{"container_type": "invalid"}, 422},
		{map[string]any{"original_name": strings.Repeat("字", 256)}, 422},
		{map[string]any{"include_deleted": true, "deleted_only": true}, 422},
		{map[string]any{"page_size": 101}, 400},
		{map[string]any{"sort": "source"}, 400},
	} {
		owner.Client.Post(URL("/me/attachments/query"), test.body).RequireStatus(t, test.status)
	}
	h.Client(t).Post(URL("/me/attachments/query"), map[string]any{}).RequireStatus(t, http.StatusUnauthorized)
	version := Str(owner.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200).Data(t), "updated_at")
	owner.Client.Delete(URL("/tasks/"+task), map[string]any{"expected_updated_at": version}).RequireStatus(t, 204)
	if Int(query(map[string]any{"include_deleted": true}).Pagination(t), "total") != 0 {
		t.Fatal("软删除任务不应泄漏其附件或计数")
	}

}

func TestMyAttachmentsRespectContainerPermissions(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "mine_acl_owner")
	member := h.RegisterAndLogin(t, "mine_acl_member")
	group := h.CreateGroup(t, owner, "权限组")
	h.AddGroupMember(t, owner, group, member.UserID)
	project := h.CreateProject(t, owner, group, "权限项目")
	h.AddProjectMember(t, owner, project, member.UserID)
	task := h.CreateTask(t, owner, project, "未共享任务", "2026-10-01", "2026-10-05")
	uploadAttachment(t, member.Client, "/projects/"+project+"/attachments", "active.txt", "text/plain", []byte("test")).RequireStatus(t, 201)
	deleted := uploadAttachment(t, member.Client, "/projects/"+project+"/attachments", "deleted.txt", "text/plain", []byte("test")).RequireStatus(t, 201)
	member.Client.Delete(URL("/attachments/"+Str(deleted.Data(t), "id")), nil).RequireStatus(t, 200)
	uploadAttachment(t, member.Client, "/tasks/"+task+"/attachments", "task.txt", "text/plain", []byte("test")).RequireStatus(t, 201)
	query := func() Response {
		return member.Client.Post(URL("/me/attachments/query"), map[string]any{"include_deleted": true, "page_size": 1}).RequireStatus(t, 200)
	}
	if Int(query().Pagination(t), "total") != 3 {
		t.Fatal("初始条数错误")
	}
	owner.Client.Delete(URL("/projects/"+project+"/members/"+member.UserID), nil).RequireStatus(t, 204)
	lost := query()
	if Int(lost.Pagination(t), "total") != 0 || len(lost.ListData(t)) != 0 {
		t.Fatal("失去容器访问权后仍泄漏附件/计数")
	}
	patch := func(visibility string) {
		version := Str(owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t), "updated_at")
		owner.Client.Patch(URL("/projects/"+project), map[string]any{"visibility": visibility, "expected_updated_at": version}).RequireStatus(t, 200)
	}
	patch("group_editable")
	if Int(query().Pagination(t), "total") != 2 {
		t.Fatal("未共享任务附件不应对组内访客显示")
	}
	version := Str(owner.Client.Get(URL("/tasks/"+task)).RequireStatus(t, 200).Data(t), "updated_at")
	owner.Client.Patch(URL("/tasks/"+task), map[string]any{"is_shared": true, "expected_updated_at": version}).RequireStatus(t, 200)
	if Int(query().Pagination(t), "total") != 3 {
		t.Fatal("共享任务附件应可见")
	}
	patch("group_readonly")
	readonly := member.Client.Post(URL("/me/attachments/query"), map[string]any{}).RequireStatus(t, 200)
	for _, row := range readonly.ListData(t) {
		if row["permissions"].(map[string]any)["can_delete"] != false {
			t.Fatal("只读共享附件不能删除")
		}
	}
	version = Str(owner.Client.Get(URL("/projects/"+project)).RequireStatus(t, 200).Data(t), "updated_at")
	owner.Client.Post(URL("/projects/"+project+"/archive"), map[string]any{"expected_updated_at": version}).RequireStatus(t, 200)
	archived := member.Client.Post(URL("/me/attachments/query"), map[string]any{"include_deleted": true}).RequireStatus(t, 200)
	for _, row := range archived.ListData(t) {
		if row["source"].(map[string]any)["read_only"] != true || row["permissions"].(map[string]any)["can_delete"] != false {
			t.Fatal("归档只读标记错误")
		}
	}
	trash := member.Client.Post(URL("/me/attachments/query"), map[string]any{"deleted_only": true}).RequireStatus(t, 200).ListData(t)
	if len(trash) != 1 || trash[0]["permissions"].(map[string]any)["can_purge"] != true {
		t.Fatal("归档中的已删除附件仍应遵循原彻底删除规则")
	}
	member.Client.Post(URL("/attachments/"+Str(deleted.Data(t), "id")+"/purge"), nil).RequireStatus(t, 204)
	// Physical task deletion is not required: the ordinary soft-delete path hides its attachments.
}
