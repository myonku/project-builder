package testsupport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 本文件提供 HTTP 层的测试辅助，供各域的集成测试复用。
//
// 目标是让每个用例只表达"业务意图"，把 JSON 编解码、Cookie 传递、
// 字段提取这些机械操作收敛到一处，避免各测试文件各写一套。

// Response 是一次 HTTP 调用的结果。
type Response struct {
	Status   int
	Body     []byte
	Recorder *httptest.ResponseRecorder
}

// Decode 解析响应体为 map。
func (r Response) Decode(t *testing.T) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(r.Body, &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（原文：%s）", err, string(r.Body))
	}
	return payload
}

// Data 取出 data 字段（对象）。
func (r Response) Data(t *testing.T) map[string]any {
	t.Helper()

	payload := r.Decode(t)
	raw, ok := payload["data"]
	if !ok {
		t.Fatalf("响应缺少 data 字段: %s", string(r.Body))
	}
	data, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %s", string(r.Body))
	}
	return data
}

// ListData 取出 data 字段（数组）。
func (r Response) ListData(t *testing.T) []map[string]any {
	t.Helper()

	payload := r.Decode(t)
	raw, ok := payload["data"]
	if !ok {
		t.Fatalf("响应缺少 data 字段: %s", string(r.Body))
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("data 不是数组: %s", string(r.Body))
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("列表元素不是对象: %s", string(r.Body))
		}
		result = append(result, object)
	}
	return result
}

// Pagination 取出 pagination 字段。
func (r Response) Pagination(t *testing.T) map[string]any {
	t.Helper()

	payload := r.Decode(t)
	raw, ok := payload["pagination"]
	if !ok {
		t.Fatalf("列表响应缺少 pagination 字段: %s", string(r.Body))
	}
	pagination, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("pagination 不是对象: %s", string(r.Body))
	}
	return pagination
}

// ErrorBody 取出 error 字段。
func (r Response) ErrorBody(t *testing.T) map[string]any {
	t.Helper()

	payload := r.Decode(t)
	raw, ok := payload["error"]
	if !ok {
		t.Fatalf("响应缺少 error 字段: %s", string(r.Body))
	}
	errBody, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("error 不是对象: %s", string(r.Body))
	}
	return errBody
}

// RequireStatus 断言状态码。
func (r Response) RequireStatus(t *testing.T, want int) Response {
	t.Helper()

	if r.Status != want {
		t.Fatalf("状态码 = %d，期望 %d（响应体：%s）", r.Status, want, string(r.Body))
	}
	return r
}

// RequireCode 断言错误码。
func (r Response) RequireCode(t *testing.T, want string) Response {
	t.Helper()

	errBody := r.ErrorBody(t)
	if got := errBody["code"]; got != want {
		t.Fatalf("error.code = %v，期望 %q（响应体：%s）", got, want, string(r.Body))
	}
	return r
}

// RequireField 断言错误信封的 fields 含指定字段。
func (r Response) RequireField(t *testing.T, name string) Response {
	t.Helper()

	errBody := r.ErrorBody(t)
	fields, _ := errBody["fields"].(map[string]any)
	if _, ok := fields[name]; !ok {
		t.Fatalf("错误字段明细缺少 %q，实际为 %v（响应体：%s）", name, fields, string(r.Body))
	}
	return r
}

// Str 从 map 中读取字符串字段。
func Str(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

// Client 是绑定到某个路由与身份的 HTTP 客户端。
type Client struct {
	t       *testing.T
	router  *gin.Engine
	cookies map[string]string
}

// Client 返回一个未登录的客户端。
func (h *Harness) Client(t *testing.T) *Client {
	t.Helper()
	return &Client{t: t, router: h.Router, cookies: map[string]string{}}
}

// WithCookie 返回携带指定会话 Cookie 的客户端副本。
func (c *Client) WithCookie(name, value string) *Client {
	cloned := &Client{t: c.t, router: c.router, cookies: map[string]string{name: value}}
	return cloned
}

// Call 执行一次请求。
//
// body 为 nil 时不发送请求体；headers 用于传递 Idempotency-Key 等自定义头。
func (c *Client) Call(method, path string, body any, headers map[string]string) Response {
	c.t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	c.router.ServeHTTP(recorder, request)

	return Response{Status: recorder.Code, Body: recorder.Body.Bytes(), Recorder: recorder}
}

// Post 是 Call(POST) 的便捷形式。
func (c *Client) Post(path string, body any) Response {
	return c.Call("POST", path, body, nil)
}

// PostWithHeaders 是带自定义头的 Call(POST)。
func (c *Client) PostWithHeaders(path string, body any, headers map[string]string) Response {
	return c.Call("POST", path, body, headers)
}

// Get 是 Call(GET) 的便捷形式。
func (c *Client) Get(path string) Response {
	return c.Call("GET", path, nil, nil)
}

// Patch 是 Call(PATCH) 的便捷形式。
func (c *Client) Patch(path string, body any) Response {
	return c.Call("PATCH", path, body, nil)
}

// Delete 是 Call(DELETE) 的便捷形式。
func (c *Client) Delete(path string, body any) Response {
	return c.Call("DELETE", path, body, nil)
}

// Authenticated 是"已注册并登录"的测试用户。
type Authenticated struct {
	UserID   string
	Username string
	Email    string
	// CookieValue 是会话 Cookie 值，用于构造其他客户端。
	CookieValue string
	// Client 是携带该会话的客户端。
	Client *Client
}

// RegisterAndLogin 注册并登录一个新用户，返回其身份与已认证客户端。
//
// 密码固定为 sharedPassword：集成测试关心的是权限与约束，不是密码策略。
func (h *Harness) RegisterAndLogin(t *testing.T, username string) Authenticated {
	t.Helper()
	return h.RegisterAndLoginWithTimezone(t, username, "UTC")
}

// RegisterAndLoginWithTimezone 与 RegisterAndLogin 相同，但显式指定注册时区。
//
// 时区会影响所有派生日期（is_overdue / overdue_task_count），
// 因此验证"按用户时区计算今天"必须能够注册非 UTC 用户。
func (h *Harness) RegisterAndLoginWithTimezone(t *testing.T, username, timezone string) Authenticated {
	t.Helper()

	client := h.Client(t)
	email := fmt.Sprintf("%s@example.com", username)

	registered := client.Post(URL("/auth/register"), map[string]any{
		"username":     username,
		"email":        email,
		"display_name": username,
		"password":     SharedPassword,
		"timezone":     timezone,
	})
	registered.RequireStatus(t, 201)
	userID := Str(registered.Data(t), "id")

	login := client.Post(URL("/auth/login"), map[string]any{
		"identifier": username,
		"password":   SharedPassword,
	})
	login.RequireStatus(t, 200)

	cookieValue := ""
	for _, cookie := range login.Recorder.Result().Cookies() {
		if cookie.Name == h.Config.Security.SessionCookieName {
			cookieValue = cookie.Value
		}
	}
	if cookieValue == "" {
		t.Fatalf("登录未返回会话 Cookie %q", h.Config.Security.SessionCookieName)
	}

	return Authenticated{
		UserID:      userID,
		Username:    username,
		Email:       email,
		CookieValue: cookieValue,
		Client:      h.Client(t).WithCookie(h.Config.Security.SessionCookieName, cookieValue),
	}
}

// CreateGroup 创建一个工作组并返回其 id，创建者成为所有者。
func (h *Harness) CreateGroup(t *testing.T, actor Authenticated, name string) string {
	t.Helper()

	response := actor.Client.Post(URL("/groups"), map[string]any{"name": name})
	response.RequireStatus(t, 201)
	return Str(response.Data(t), "id")
}

// AddGroupMember 由所有者把目标用户加入工作组。
func (h *Harness) AddGroupMember(t *testing.T, owner Authenticated, groupID, userID string) {
	t.Helper()

	response := owner.Client.Post(URL("/groups/"+groupID+"/members"),
		map[string]any{"user_id": userID})
	response.RequireStatus(t, 201)
}

// CreateProject 在指定工作组下创建项目并返回其 id。
func (h *Harness) CreateProject(t *testing.T, actor Authenticated, groupID, name string) string {
	t.Helper()

	response := actor.Client.Post(URL("/projects"), map[string]any{
		"name":       name,
		"group_id":   groupID,
		"start_date": "2026-10-01",
		"end_date":   "2026-10-31",
	})
	response.RequireStatus(t, 201)
	return Str(response.Data(t), "id")
}

// CreateTask 在指定项目下创建任务并返回其 id。
func (h *Harness) CreateTask(t *testing.T, actor Authenticated, projectID, name, start, end string) string {
	t.Helper()

	response := actor.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
		"name":       name,
		"start_date": start,
		"end_date":   end,
	})
	response.RequireStatus(t, 201)
	return Str(response.Data(t), "id")
}

// SharedPassword 是集成测试统一使用的密码。
const SharedPassword = "correct horse battery"
