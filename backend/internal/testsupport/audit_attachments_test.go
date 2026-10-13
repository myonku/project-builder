package testsupport

import (
	"strings"
	"testing"
)

// TestAuditCoversAttachmentWrites 覆盖附件域的三条写路径：上传、逻辑删除、彻底删除。
//
// 附件是"带字节的对象"：审计只记元数据，绝不记内容——因此这里额外断言
// 记录里不会出现文件内容。
func TestAuditCoversAttachmentWrites(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "audit_att_owner")
	groupID := harness.CreateGroup(t, owner, "审计附件组")
	projectID := harness.CreateProject(t, owner, groupID, "审计附件项目")

	const content = "attachment-payload-that-must-not-be-audited"
	uploaded := uploadAttachment(t, owner.Client,
		"/projects/"+projectID+"/attachments", "审计附件.txt", "text/plain", []byte(content)).
		RequireStatus(t, 201)
	attachmentID := Str(uploaded.Data(t), "id")

	rows := auditRowsFor(t, harness, attachmentID)
	created := requireAuditRow(t, rows, "created", "审计附件.txt")
	if created.ActorID != owner.UserID {
		t.Fatalf("上传记录的 actor_id = %s，期望 %s", created.ActorID, owner.UserID)
	}
	if created.RequestID == "" {
		t.Fatal("上传记录缺少 request_id")
	}
	for _, row := range rows {
		if strings.Contains(row.Changes, content) {
			t.Fatalf("附件内容被写进了审计记录: %s", row.Changes)
		}
	}

	// 逻辑删除。
	owner.Client.Delete(URL("/attachments/"+attachmentID), nil).RequireStatus(t, 200)
	rows = auditRowsFor(t, harness, attachmentID)
	requireAuditRow(t, rows, "deleted")

	// 彻底删除（不可逆动作，必须有记录）。
	owner.Client.Post(URL("/attachments/"+attachmentID+"/purge"), map[string]any{}).
		RequireStatus(t, 204)
	rows = auditRowsFor(t, harness, attachmentID)
	requireAuditRow(t, rows, "purged", "审计附件.txt")

	// 未逻辑删除时不允许彻底删除：不留记录。
	second := uploadAttachment(t, owner.Client,
		"/projects/"+projectID+"/attachments", "第二个.txt", "text/plain", []byte("x")).
		RequireStatus(t, 201)
	secondID := Str(second.Data(t), "id")
	before := countAuditRowsFor(t, harness, secondID)

	owner.Client.Post(URL("/attachments/"+secondID+"/purge"), map[string]any{}).
		RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, secondID); after != before {
		t.Fatalf("被拒的彻底删除留下了 %d 条新记录（期望 0）", after-before)
	}
}
