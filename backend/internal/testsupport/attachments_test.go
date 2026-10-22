package testsupport

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// multipartBody 构造一个单文件 multipart 请求体。
func multipartBody(t *testing.T, field, filename, contentType string, content []byte) ([]byte, string) {
	t.Helper()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename),
	}
	if contentType != "" {
		header["Content-Type"] = []string{contentType}
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("构造 multipart 分片失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 写入器失败: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

// uploadAttachment 通过真实 HTTP 端点上传一个附件。
func uploadAttachment(
	t *testing.T, client *Client, path, filename, contentType string, content []byte,
) Response {
	t.Helper()

	body, formType := multipartBody(t, "file", filename, contentType, content)
	return client.PostRaw(URL(path), body, formType)
}

// TestAttachmentUploadAndDownload 覆盖上传、列表与下载的正常路径。
func TestAttachmentUploadAndDownload(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "att_owner")
	groupID := harness.CreateGroup(t, owner, "附件组")
	projectID := harness.CreateProject(t, owner, groupID, "附件项目")
	taskID := harness.CreateTask(t, owner, projectID, "附件任务", "2026-10-01", "2026-10-05")

	payload := []byte("项目附件的正文内容")

	uploaded := uploadAttachment(t, owner.Client,
		"/projects/"+projectID+"/attachments", "周报 2026-10.txt", "text/plain", payload)
	uploaded.RequireStatus(t, http.StatusCreated)

	data := uploaded.Data(t)
	attachmentID := Str(data, "id")
	if attachmentID == "" {
		t.Fatal("上传响应缺少 id")
	}
	if got := Str(data, "original_name"); got != "周报 2026-10.txt" {
		t.Fatalf("original_name = %q，期望保留原始文件名", got)
	}
	if got := Str(data, "extension"); got != ".txt" {
		t.Fatalf("extension = %q，期望 .txt", got)
	}
	if got := Int(data, "size_bytes"); got != len(payload) {
		t.Fatalf("size_bytes = %d，期望 %d", got, len(payload))
	}
	if got := Str(data, "project_id"); got != projectID {
		t.Fatalf("project_id = %q，期望 %q", got, projectID)
	}
	// 存储标识是实现细节，不得出现在响应里。
	if _, exists := data["stored_key"]; exists {
		t.Error("响应不应包含 stored_key")
	}

	t.Run("列出附件", func(t *testing.T) {
		listed := owner.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20}).RequireStatus(t, http.StatusOK)
		if got := Int(listed.Pagination(t), "total"); got != 1 {
			t.Fatalf("total = %d，期望 1", got)
		}
	})

	t.Run("下载内容与上传一致", func(t *testing.T) {
		response := owner.Client.GetRaw(URL("/attachments/" + attachmentID + "/download"))
		response.RequireStatus(t, http.StatusOK)
		if !bytes.Equal(response.Body, payload) {
			t.Fatalf("下载内容 = %q，期望 %q", response.Body, payload)
		}
		if got := response.Recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
			t.Fatalf("Content-Type = %q，期望 text/plain", got)
		}
		// 中文文件名必须通过 RFC 5987 的 filename* 正确表达。
		disposition := response.Recorder.Header().Get("Content-Disposition")
		if !strings.Contains(disposition, "filename*=UTF-8''") {
			t.Fatalf("Content-Disposition 缺少 filename*: %q", disposition)
		}
		if !strings.Contains(disposition, `filename="`) {
			t.Fatalf("Content-Disposition 缺少 ASCII 回退名: %q", disposition)
		}
	})

	t.Run("任务附件", func(t *testing.T) {
		created := uploadAttachment(t, owner.Client,
			"/tasks/"+taskID+"/attachments", "设计稿.pdf", "application/pdf", []byte("%PDF-1.4"))
		created.RequireStatus(t, http.StatusCreated)
		if got := Str(created.Data(t), "task_id"); got != taskID {
			t.Fatalf("task_id = %q，期望 %q", got, taskID)
		}
	})

	t.Run("项目列表不含任务的附件", func(t *testing.T) {
		listed := owner.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20}).RequireStatus(t, http.StatusOK)
		// 归属过滤必须在 SQL 里完成，而不是取回全部再筛。
		if got := len(listed.ListData(t)); got != 1 {
			t.Fatalf("项目附件数 = %d，期望 1（任务的附件不应出现）", got)
		}
	})

	t.Run("排序字段非法返回 400", func(t *testing.T) {
		owner.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20, "sort": "stored_key"}).
			RequireStatus(t, http.StatusBadRequest)
	})

	t.Run("非成员看不到附件", func(t *testing.T) {
		outsider := harness.RegisterAndLogin(t, "att_outsider")
		outsider.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20}).RequireStatus(t, http.StatusNotFound)
		outsider.Client.GetRaw(URL("/attachments/"+attachmentID+"/download")).
			RequireStatus(t, http.StatusNotFound)
	})
}

// TestAttachmentUploadRejections 覆盖上传的三类拒绝：扩展名、大小、归属。
func TestAttachmentUploadRejections(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "attrej_owner")
	groupID := harness.CreateGroup(t, owner, "附件拒绝组")
	projectID := harness.CreateProject(t, owner, groupID, "附件拒绝项目")

	t.Run("超过大小上限返回 413", func(t *testing.T) {
		oversized := bytes.Repeat([]byte("x"), int(testMaxFileSize)+1024)
		response := uploadAttachment(t, owner.Client,
			"/projects/"+projectID+"/attachments", "big.bin", "application/octet-stream", oversized)
		response.RequireStatus(t, http.StatusRequestEntityTooLarge)
		response.RequireCode(t, "file_too_large")
	})

	t.Run("空文件返回 422", func(t *testing.T) {
		response := uploadAttachment(t, owner.Client,
			"/projects/"+projectID+"/attachments", "empty.txt", "text/plain", nil)
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "file")
	})

	t.Run("缺少 file 字段返回 422", func(t *testing.T) {
		body, formType := multipartBody(t, "wrong_field", "a.txt", "text/plain", []byte("x"))
		owner.Client.PostRaw(URL("/projects/"+projectID+"/attachments"), body, formType).
			RequireStatus(t, http.StatusUnprocessableEntity)
	})

	t.Run("非 multipart 请求返回 400", func(t *testing.T) {
		owner.Client.PostRaw(URL("/projects/"+projectID+"/attachments"),
			[]byte(`{"file":"x"}`), "application/json").
			RequireStatus(t, http.StatusBadRequest)
	})

	t.Run("不存在的项目返回 404", func(t *testing.T) {
		uploadAttachment(t, owner.Client,
			"/projects/01a1290b-0ae8-7046-9106-30f2d07815af/attachments",
			"a.txt", "text/plain", []byte("x")).
			RequireStatus(t, http.StatusNotFound)
	})

	t.Run("非成员不能上传", func(t *testing.T) {
		outsider := harness.RegisterAndLogin(t, "attrej_outsider")
		uploadAttachment(t, outsider.Client,
			"/projects/"+projectID+"/attachments", "a.txt", "text/plain", []byte("x")).
			RequireStatus(t, http.StatusNotFound)
	})

	t.Run("归档项目不可上传", func(t *testing.T) {
		version := Str(owner.Client.Get(URL("/projects/"+projectID)).
			RequireStatus(t, http.StatusOK).Data(t), "updated_at")
		owner.Client.Post(URL("/projects/"+projectID+"/archive"),
			map[string]any{"expected_updated_at": version}).RequireStatus(t, http.StatusOK)

		uploadAttachment(t, owner.Client,
			"/projects/"+projectID+"/attachments", "a.txt", "text/plain", []byte("x")).
			RequireStatus(t, http.StatusForbidden)
	})
}

// TestAttachmentExtensionWhitelist 覆盖全局扩展名白名单的"不限制"一侧。
//
// 具体白名单的内部判定（大小写、无扩展名、命中与否）由
// internal/domain/attachment_test.go 覆盖：那是纯函数，不需要起服务。
// 集成层只验证"配置为空时确实不拦"，因为这是默认配置的行为。
func TestAttachmentExtensionWhitelist(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "attwl_owner")
	groupID := harness.CreateGroup(t, owner, "白名单组")
	projectID := harness.CreateProject(t, owner, groupID, "白名单项目")

	// 白名单为空 = 不限制。
	t.Run("白名单为空时任何扩展名都允许", func(t *testing.T) {
		uploadAttachment(t, owner.Client,
			"/projects/"+projectID+"/attachments", "任意.exe", "application/octet-stream",
			[]byte("x")).RequireStatus(t, http.StatusCreated)
	})

}

// TestAttachmentQuota 覆盖"单用户最多 N 份附件"。
func TestAttachmentQuota(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "attquota_owner")
	groupID := harness.CreateGroup(t, owner, "配额组")
	projectID := harness.CreateProject(t, owner, groupID, "配额项目")

	// 逐个上传到上限。
	for index := range testMaxFilesPerUser {
		uploadAttachment(t, owner.Client, "/projects/"+projectID+"/attachments",
			fmt.Sprintf("file-%d.txt", index), "text/plain", []byte("x")).
			RequireStatus(t, http.StatusCreated)
	}

	// 第 N+1 个被拒绝（409：状态冲突，而不是字段格式问题）。
	over := uploadAttachment(t, owner.Client, "/projects/"+projectID+"/attachments",
		"one-too-many.txt", "text/plain", []byte("x"))
	over.RequireStatus(t, http.StatusConflict)
	over.RequireCode(t, "conflict")

	// 逻辑删除**不**释放配额：字节仍在磁盘上。
	var deletedID string
	listed := owner.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
		map[string]any{"page": 1, "page_size": 20}).RequireStatus(t, http.StatusOK)
	deletedID = Str(listed.ListData(t)[0], "id")
	owner.Client.Delete(URL("/attachments/"+deletedID), nil).RequireStatus(t, http.StatusOK)

	uploadAttachment(t, owner.Client, "/projects/"+projectID+"/attachments",
		"after-delete.txt", "text/plain", []byte("x")).
		RequireStatus(t, http.StatusConflict)

	// 彻底删除后配额释放。
	owner.Client.Post(URL("/attachments/"+deletedID+"/purge"), nil).
		RequireStatus(t, http.StatusNoContent)
	uploadAttachment(t, owner.Client, "/projects/"+projectID+"/attachments",
		"after-purge.txt", "text/plain", []byte("x")).
		RequireStatus(t, http.StatusCreated)
}

// TestAttachmentSoftDeleteVisibility 覆盖逻辑删除后的可见性与彻底删除。
//
// 用户确认的规则：逻辑删除后的附件只对**上传者**、**持有者**与**系统管理员**可见。
func TestAttachmentSoftDeleteVisibility(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "attdel_owner")
	member := harness.RegisterAndLogin(t, "attdel_member")
	admin := harness.RegisterAndLogin(t, "attdel_admin")
	promoteToAdmin(t, harness, admin.UserID)

	groupID := harness.CreateGroup(t, owner, "删除可见性组")
	projectID := harness.CreateProject(t, owner, groupID, "删除可见性项目")
	harness.AddProjectMember(t, owner, projectID, member.UserID)

	// 由**普通成员**上传：这样"上传者"与"持有者"是两个不同的人。
	payload := []byte("member 上传的内容")
	created := uploadAttachment(t, member.Client, "/projects/"+projectID+"/attachments",
		"member-file.txt", "text/plain", payload).RequireStatus(t, http.StatusCreated)
	attachmentID := Str(created.Data(t), "id")

	member.Client.Delete(URL("/attachments/"+attachmentID), nil).RequireStatus(t, http.StatusOK)

	t.Run("上传者仍可见", func(t *testing.T) {
		member.Client.GetRaw(URL("/attachments/"+attachmentID+"/download")).
			RequireStatus(t, http.StatusOK)

		// 默认列表不含已删除的；deleted_only 才看得到。
		listed := member.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20}).
			RequireStatus(t, http.StatusOK)
		if got := len(listed.ListData(t)); got != 0 {
			t.Fatalf("默认列表应不含已删除附件，实际 %d 条", got)
		}
		only := member.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20, "deleted_only": true}).
			RequireStatus(t, http.StatusOK)
		if got := Int(only.Pagination(t), "total"); got != 1 {
			t.Fatalf("deleted_only 的 total = %d，期望 1", got)
		}
	})

	t.Run("持有者可见", func(t *testing.T) {
		owner.Client.GetRaw(URL("/attachments/"+attachmentID+"/download")).
			RequireStatus(t, http.StatusOK)
		listed := owner.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20, "include_deleted": true}).
			RequireStatus(t, http.StatusOK)
		if got := Int(listed.Pagination(t), "total"); got != 1 {
			t.Fatalf("持有者 include_deleted 的 total = %d，期望 1", got)
		}
	})

	t.Run("系统管理员可见", func(t *testing.T) {
		admin.Client.GetRaw(URL("/attachments/"+attachmentID+"/download")).
			RequireStatus(t, http.StatusOK)
	})

	t.Run("其他普通成员不可见", func(t *testing.T) {
		other := harness.RegisterAndLogin(t, "attdel_other")
		harness.AddProjectMember(t, owner, projectID, other.UserID)

		other.Client.GetRaw(URL("/attachments/"+attachmentID+"/download")).
			RequireStatus(t, http.StatusNotFound)
		listed := other.Client.Post(URL("/projects/"+projectID+"/attachments/query"),
			map[string]any{"page": 1, "page_size": 20, "include_deleted": true}).
			RequireStatus(t, http.StatusOK)
		// 计数与列表必须同源：别人看不到的行既不能出现在列表里，也不能计入 total。
		if got := Int(listed.Pagination(t), "total"); got != 0 {
			t.Fatalf("其他成员 include_deleted 的 total = %d，期望 0", got)
		}
		if got := len(listed.ListData(t)); got != 0 {
			t.Fatalf("其他成员不应看到已删除附件，实际 %d 条", got)
		}
	})

	t.Run("未逻辑删除的附件不能彻底删除", func(t *testing.T) {
		active := uploadAttachment(t, member.Client, "/projects/"+projectID+"/attachments",
			"active.txt", "text/plain", []byte("x")).RequireStatus(t, http.StatusCreated)
		response := member.Client.Post(URL("/attachments/"+Str(active.Data(t), "id")+"/purge"), nil)
		response.RequireStatus(t, http.StatusConflict)
		response.RequireField(t, "attachment_id")
	})

	t.Run("彻底删除后行与字节都消失", func(t *testing.T) {
		// 记录存储标识，用于确认字节真的被清掉了。
		var storedKey string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT stored_key FROM attachments WHERE id = $1`, attachmentID).Scan(&storedKey); err != nil {
			t.Fatalf("查询 stored_key 失败: %v", err)
		}
		exists, err := harness.Storage.Exists(context.Background(), storedKey)
		if err != nil || !exists {
			t.Fatalf("彻底删除前对象应存在，实际 exists=%v err=%v", exists, err)
		}

		member.Client.Post(URL("/attachments/"+attachmentID+"/purge"), nil).
			RequireStatus(t, http.StatusNoContent)

		var rows int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM attachments WHERE id = $1`, attachmentID).Scan(&rows); err != nil {
			t.Fatalf("统计附件失败: %v", err)
		}
		if rows != 0 {
			t.Fatalf("彻底删除后仍剩 %d 行", rows)
		}
		exists, err = harness.Storage.Exists(context.Background(), storedKey)
		if err != nil {
			t.Fatalf("查询对象失败: %v", err)
		}
		if exists {
			t.Fatal("彻底删除后磁盘上仍残留对象")
		}

		// 再删一次：行已不存在。
		member.Client.Post(URL("/attachments/"+attachmentID+"/purge"), nil).
			RequireStatus(t, http.StatusNotFound)
	})

	t.Run("非上传者也不能彻底删除别人的附件", func(t *testing.T) {
		created := uploadAttachment(t, member.Client, "/projects/"+projectID+"/attachments",
			"member-file-2.txt", "text/plain", []byte("x")).RequireStatus(t, http.StatusCreated)
		id := Str(created.Data(t), "id")
		member.Client.Delete(URL("/attachments/"+id), nil).RequireStatus(t, http.StatusOK)

		other := harness.RegisterAndLogin(t, "attdel_other2")
		harness.AddProjectMember(t, owner, projectID, other.UserID)
		other.Client.Post(URL("/attachments/"+id+"/purge"), nil).
			RequireStatus(t, http.StatusNotFound)

		// 持有者可以收拾成员留下的已删除附件（否则这些字节永远无人能清理）。
		owner.Client.Post(URL("/attachments/"+id+"/purge"), nil).
			RequireStatus(t, http.StatusNoContent)
	})
}

func TestAttachmentUploadHiddenContainerErrorCode(t *testing.T) {
	h := NewHarness(t)
	owner := h.RegisterAndLogin(t, "hidden_owner")
	other := h.RegisterAndLogin(t, "hidden_other")
	group := h.CreateGroup(t, owner, "hidden group")
	project := h.CreateProject(t, owner, group, "hidden project")
	task := h.CreateTask(t, owner, project, "hidden task", "2026-10-01", "2026-10-05")
	uploadAttachment(t, other.Client, "/projects/"+project+"/attachments", "hidden.txt", "text/plain", []byte("private")).RequireStatus(t, 404).RequireCode(t, "project_not_found")
	uploadAttachment(t, other.Client, "/tasks/"+task+"/attachments", "hidden.txt", "text/plain", []byte("private")).RequireStatus(t, 404).RequireCode(t, "task_not_found")
}
