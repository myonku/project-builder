package testsupport

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"testing"
)

// avatarPNG 生成一张纯色 PNG，尺寸由调用方指定。
func avatarPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0x2e, G: 0x7d, B: 0x32, A: 0xff})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("生成 PNG 失败: %v", err)
	}
	return buffer.Bytes()
}

// avatarJPEG 生成一张纯色 JPEG。
func avatarJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 0x15, G: 0x65, B: 0xc0, A: 0xff})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("生成 JPEG 失败: %v", err)
	}
	return buffer.Bytes()
}

// multipartFile 构造单文件 multipart 请求体。
func multipartFile(t *testing.T, field, filename string, content []byte) ([]byte, string) {
	t.Helper()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("创建 multipart 字段失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

// avatarKey 读取用户当前的头像存储 key（测试直接查库核对对象生命周期）。
func avatarKey(t *testing.T, harness *Harness, userID string) string {
	t.Helper()

	var key *string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT avatar_stored_key FROM users WHERE id = $1`, userID).Scan(&key); err != nil {
		t.Fatalf("读取头像 key 失败: %v", err)
	}
	if key == nil {
		return ""
	}
	return *key
}

func TestAvatarLifecycleAndProjection(t *testing.T) {
	harness := NewHarness(t)
	alice := harness.RegisterAndLogin(t, "alice_avatar")

	// 初始：没有头像，profile 的 avatar_updated_at 为 null。
	profile := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200).Data(t)
	if profile["avatar_updated_at"] != nil {
		t.Fatalf("初始 avatar_updated_at 应为 null，实际 %v", profile["avatar_updated_at"])
	}
	updatedAtBefore := Str(profile, "updated_at")

	alice.Client.Get(URL("/users/"+alice.UserID+"/avatar")).
		RequireStatus(t, 404).RequireCode(t, "avatar_not_found")

	// 上传 PNG。
	pngBytes := avatarPNG(t, 4, 4)
	body, contentType := multipartFile(t, "file", "avatar.png", pngBytes)
	uploaded := alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200).Data(t)
	avatarUpdatedAt := Str(uploaded, "avatar_updated_at")
	if avatarUpdatedAt == "" {
		t.Fatal("上传成功后 avatar_updated_at 必须非空")
	}
	if got := Str(uploaded, "updated_at"); got != updatedAtBefore {
		t.Fatalf("头像上传不得推进 users.updated_at：before=%s after=%s", updatedAtBefore, got)
	}
	keyAfterUpload := avatarKey(t, harness, alice.UserID)
	if keyAfterUpload == "" {
		t.Fatal("上传后数据库应有 avatar_stored_key")
	}

	// 下载：原始字节、Content-Type、缓存头、ETag/304。
	download := alice.Client.Get(URL("/users/"+alice.UserID+"/avatar")).RequireStatus(t, 200)
	if got := download.Recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q，期望 image/png", got)
	}
	if got := download.Recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("缺少 nosniff，实际 %q", got)
	}
	if got := download.Recorder.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Fatalf("Cache-Control = %q", got)
	}
	etag := download.Recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("头像响应缺少 ETag")
	}
	if !bytes.Equal(download.Body, pngBytes) {
		t.Fatalf("下载字节与上传不一致：got %d bytes, want %d", len(download.Body), len(pngBytes))
	}
	// 带 If-None-Match 的原始请求验证 304。
	conditional := alice.Client.CallRawWithHeaders(
		http.MethodGet, URL("/users/"+alice.UserID+"/avatar"), nil, "", map[string]string{"If-None-Match": etag},
	)
	conditional.RequireStatus(t, 304)

	// 替换头像：旧对象应被删除，profile 版本更新。
	jpegBytes := avatarJPEG(t)
	body, contentType = multipartFile(t, "file", "avatar.jpg", jpegBytes)
	replaced := alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200).Data(t)
	if Str(replaced, "avatar_updated_at") == avatarUpdatedAt {
		t.Fatal("替换头像后 avatar_updated_at 必须变化")
	}
	if keyAfterUpload == avatarKey(t, harness, alice.UserID) {
		t.Fatal("替换头像必须写入新的存储 key")
	}
	exists, err := harness.Storage.Exists(context.Background(), keyAfterUpload)
	if err != nil {
		t.Fatalf("查询旧对象失败: %v", err)
	}
	if exists {
		t.Fatal("替换头像后旧对象应被删除")
	}
	alice.Client.Get(URL("/users/"+alice.UserID+"/avatar")).RequireStatus(t, 200)

	// 清除头像：幂等，且不推进 users.updated_at。
	alice.Client.Delete(URL("/me/avatar"), nil).RequireStatus(t, 204)
	alice.Client.Delete(URL("/me/avatar"), nil).RequireStatus(t, 204)
	cleared := alice.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200).Data(t)
	if cleared["avatar_updated_at"] != nil {
		t.Fatalf("清除后 avatar_updated_at 应为 null，实际 %v", cleared["avatar_updated_at"])
	}
	if got := Str(cleared, "updated_at"); got != updatedAtBefore {
		t.Fatalf("清除头像不得推进 users.updated_at：before=%s after=%s", updatedAtBefore, got)
	}
	alice.Client.Get(URL("/users/"+alice.UserID+"/avatar")).
		RequireStatus(t, 404).RequireCode(t, "avatar_not_found")
}

func TestAvatarVisibilityFollowsProfile(t *testing.T) {
	harness := NewHarness(t)
	alice := harness.RegisterAndLogin(t, "alice_avatar_visibility")
	bob := harness.RegisterAndLogin(t, "bob_avatar_visibility")

	body, contentType := multipartFile(t, "file", "avatar.png", avatarPNG(t, 2, 2))
	alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200)

	// 无协作关系：不可见，与资料端点同码。
	bob.Client.Get(URL("/users/"+alice.UserID+"/avatar")).
		RequireStatus(t, 404).RequireCode(t, "user_not_found")

	// 同组后可见。
	group := alice.Client.Post(URL("/groups"), map[string]any{"name": "Avatar 可见性"}).
		RequireStatus(t, 201).Data(t)
	groupID := Str(group, "id")
	alice.Client.Post(URL("/groups/"+groupID+"/members"), map[string]any{"user_id": bob.UserID}).
		RequireStatus(t, 201)
	bob.Client.Get(URL("/users/"+alice.UserID+"/avatar")).RequireStatus(t, 200)

	// 成员投影必须带 avatar_updated_at。
	members := bob.Client.Post(URL("/groups/"+groupID+"/members/query"), map[string]any{}).
		RequireStatus(t, 200).ListData(t)
	found := false
	for _, member := range members {
		if Str(member, "user_id") == alice.UserID {
			found = Str(member, "avatar_updated_at") != ""
		}
	}
	if !found {
		t.Fatalf("工作组成员投影缺少 avatar_updated_at：%v", members)
	}

	// 头像端点只作用于本人：bob 上传只会写 bob，不会改 alice。
	body, contentType = multipartFile(t, "file", "bob.png", avatarPNG(t, 3, 3))
	bob.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200)
	aliceProfile := bob.Client.Get(URL("/users/"+alice.UserID)).RequireStatus(t, 200).Data(t)
	if Str(aliceProfile, "avatar_updated_at") == "" {
		t.Fatal("bob 上传不应清除 alice 的头像")
	}

	// 管理员可读任意用户头像。
	admin := harness.RegisterAndLogin(t, "admin_avatar_visibility")
	promoteToAdmin(t, harness, admin.UserID)
	admin.Client.Get(URL("/users/"+alice.UserID+"/avatar")).RequireStatus(t, 200)
}

func TestPlanViewMemberAvatarProjection(t *testing.T) {
	harness := NewHarness(t)
	alice := harness.RegisterAndLogin(t, "alice_avatar_planview")

	body, contentType := multipartFile(t, "file", "avatar.png", avatarPNG(t, 2, 2))
	uploaded := alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200).Data(t)
	avatarVersion := Str(uploaded, "avatar_updated_at")

	groupID := harness.CreateGroup(t, alice, "头像计划视图")
	projectID := harness.CreateProject(t, alice, groupID, "头像计划视图项目")
	taskID := harness.CreateTask(t, alice, projectID, "头像计划视图任务", "2026-10-01", "2026-10-05")

	task := planViewTask(t, alice.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200), taskID)
	owner, ok := task["owner"].(map[string]any)
	if !ok {
		t.Fatalf("任务 owner 不是对象: %v", task["owner"])
	}
	if Str(owner, "avatar_updated_at") != avatarVersion {
		t.Fatalf("owner.avatar_updated_at = %v，期望 %s", owner["avatar_updated_at"], avatarVersion)
	}
	members, ok := task["members"].([]any)
	if !ok || len(members) == 0 {
		t.Fatalf("任务 members 为空: %v", task["members"])
	}
	first, ok := members[0].(map[string]any)
	if !ok || Str(first, "avatar_updated_at") != avatarVersion {
		t.Fatalf("成员投影缺少 avatar_updated_at: %v", members[0])
	}
}

func TestAvatarValidation(t *testing.T) {
	harness := NewHarness(t)
	alice := harness.RegisterAndLogin(t, "alice_avatar_validation")

	cases := []struct {
		name       string
		filename   string
		content    []byte
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name: "文本不是图片", filename: "a.txt", content: []byte("not an image"),
			wantStatus: 415, wantCode: "unsupported_media_type", wantField: "file",
		},
		{
			name: "GIF 不支持", filename: "a.gif",
			content:    []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
			wantStatus: 415, wantCode: "unsupported_media_type", wantField: "file",
		},
		{
			name: "WebP 不支持", filename: "a.webp",
			content:    []byte("RIFF\x1a\x00\x00\x00WEBPVP8 " + string(make([]byte, 20))),
			wantStatus: 415, wantCode: "unsupported_media_type", wantField: "file",
		},
		{
			name: "尺寸超过 4096", filename: "large.png", content: avatarPNG(t, 4097, 1),
			wantStatus: 422, wantCode: "validation_error", wantField: "file",
		},
		{
			name: "空文件", filename: "empty.png", content: nil,
			wantStatus: 422, wantCode: "validation_error", wantField: "file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := multipartFile(t, "file", tc.filename, tc.content)
			alice.Client.PostRaw(URL("/me/avatar"), body, contentType).
				RequireStatus(t, tc.wantStatus).RequireCode(t, tc.wantCode).RequireField(t, tc.wantField)
		})
	}

	// 超过 2 MiB：413。
	oversized := make([]byte, harness.Config.Storage.AvatarMaxFileSize+1)
	body, contentType := multipartFile(t, "file", "huge.png", oversized)
	alice.Client.PostRaw(URL("/me/avatar"), body, contentType).
		RequireStatus(t, 413).RequireCode(t, "file_too_large").RequireField(t, "file")

	// 缺少 file 字段：422。
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("other", "value")
	_ = writer.Close()
	alice.Client.PostRaw(URL("/me/avatar"), buffer.Bytes(), writer.FormDataContentType()).
		RequireStatus(t, 422).RequireCode(t, "validation_error").RequireField(t, "file")
}

func TestAvatarAuditAndOldObjectCleanup(t *testing.T) {
	harness := NewHarness(t)
	alice := harness.RegisterAndLogin(t, "alice_avatar_audit")

	body, contentType := multipartFile(t, "file", "avatar.png", avatarPNG(t, 2, 2))
	alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200)
	firstKey := avatarKey(t, harness, alice.UserID)

	// 再上传一次：旧对象删除，审计记录 avatar=set。
	body, contentType = multipartFile(t, "file", "avatar2.png", avatarPNG(t, 5, 5))
	alice.Client.PostRaw(URL("/me/avatar"), body, contentType).RequireStatus(t, 200)
	secondKey := avatarKey(t, harness, alice.UserID)
	if secondKey == firstKey {
		t.Fatal("第二次上传必须使用新 key")
	}
	exists, err := harness.Storage.Exists(context.Background(), firstKey)
	if err != nil {
		t.Fatalf("查询旧对象失败: %v", err)
	}
	if exists {
		t.Fatal("第二次上传后旧对象应被删除")
	}

	// 清除：审计记录 avatar=cleared，对象删除。
	alice.Client.Delete(URL("/me/avatar"), nil).RequireStatus(t, 204)
	if avatarKey(t, harness, alice.UserID) != "" {
		t.Fatal("清除后 avatar_stored_key 应为空")
	}
	exists, err = harness.Storage.Exists(context.Background(), secondKey)
	if err != nil {
		t.Fatalf("查询被清除对象失败: %v", err)
	}
	if exists {
		t.Fatal("清除头像后对象应被删除")
	}

	rows, err := harness.Pool.Query(context.Background(), `
		SELECT changes->>'avatar'
		  FROM activity_logs
		 WHERE object_type = 'user' AND object_id = $1 AND action = 'updated'
		 ORDER BY created_at`, alice.UserID)
	if err != nil {
		t.Fatalf("查询头像审计失败: %v", err)
	}
	defer rows.Close()

	var changes []string
	for rows.Next() {
		var value *string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("读取审计记录失败: %v", err)
		}
		if value != nil {
			changes = append(changes, *value)
		}
	}
	if len(changes) != 3 || changes[0] != "set" || changes[1] != "set" || changes[2] != "cleared" {
		t.Fatalf("头像审计 changes 应为 [set set cleared]，实际 %v", changes)
	}
}
