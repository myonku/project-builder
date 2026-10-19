package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/testsupport"
)

// TestRegistrationApprovalFlow 覆盖"提交申请 → 管理员审核 → 放行/拒绝"的完整 HTTP 链路。
func TestRegistrationApprovalFlow(t *testing.T) {
	harness := testsupport.NewHarnessWithOptions(t, testsupport.HarnessOptions{
		RegistrationApproval: true,
	})
	ctx := context.Background()

	// 造一个系统管理员（审核端点需要会话）。
	admin, err := harness.Auth.CreateAccount(ctx, domain.RegistrationInput{
		Username:    "registrar",
		Email:       "registrar@example.com",
		DisplayName: "Registrar",
		Password:    "registrar password",
	}, "")
	if err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	if _, err := harness.Users.UpdateSystemRole(ctx, admin.ID, domain.UserSystemRoleAdmin); err != nil {
		t.Fatalf("提升管理员失败: %v", err)
	}
	login := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"), map[string]any{
		"identifier": "registrar",
		"password":   "registrar password",
	}, nil)
	requireStatus(t, login, http.StatusOK)
	cookie := sessionCookie(t, login, harness.Config.Security.SessionCookieName)
	authHeaders := map[string]string{
		"Cookie": fmt.Sprintf("%s=%s", harness.Config.Security.SessionCookieName, cookie),
	}

	// 1) 提交申请：不建用户、不发 Cookie。
	register := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("applicant", "applicant@example.com"), nil)
	requireStatus(t, register, http.StatusCreated)
	requestData := dataOf(t, register)
	if requestData["status"] != domain.RegistrationStatusPending {
		t.Fatalf("申请状态 = %v，期望 pending", requestData["status"])
	}
	requestID, _ := requestData["id"].(string)
	if requestID == "" {
		t.Fatal("申请响应缺少 id")
	}
	if len(register.Result().Cookies()) != 0 {
		t.Fatal("提交注册申请不应设置会话 Cookie")
	}

	// 申请未通过前不能登录。
	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"), map[string]any{
		"identifier": "applicant",
		"password":   "correct horse battery",
	}, nil), http.StatusUnauthorized)

	// 2) 管理员能看到待审核列表。
	list := request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/query"),
		map[string]any{"status": "pending"}, authHeaders)
	requireStatus(t, list, http.StatusOK)
	listPayload := decode(t, list)
	items, _ := listPayload["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("待审核列表 = %d 条，期望 1", len(items))
	}

	// 3) 审核通过：返回用户，随后可以登录。
	review := request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/"+requestID+"/review"),
		map[string]any{"status": "approved"}, authHeaders)
	requireStatus(t, review, http.StatusOK)
	reviewData := dataOf(t, review)
	if reviewData["user"] == nil {
		t.Fatalf("通过审核应返回 user: %s", review.Body.String())
	}

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"), map[string]any{
		"identifier": "applicant",
		"password":   "correct horse battery",
	}, nil), http.StatusOK)

	// 4) 重复审核返回 409。
	requireStatus(t, request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/"+requestID+"/review"),
		map[string]any{"status": "approved"}, authHeaders), http.StatusConflict)

	// 5) 待审核期间重复提交同名申请返回 409。
	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("waiting", "waiting@example.com"), nil), http.StatusCreated)
	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("WAITING", "another@example.com"), nil), http.StatusConflict)

	// 6) 拒绝另一条申请：不能登录，申请状态为 rejected。
	list = request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/query"),
		map[string]any{"status": "pending"}, authHeaders)
	requireStatus(t, list, http.StatusOK)
	listPayload = decode(t, list)
	items, _ = listPayload["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("拒绝前待审核列表 = %d 条，期望 1", len(items))
	}
	pending, _ := items[0].(map[string]any)
	pendingID, _ := pending["id"].(string)
	reject := request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/"+pendingID+"/review"),
		map[string]any{"status": "rejected", "reason": "资料不完整"}, authHeaders)
	requireStatus(t, reject, http.StatusOK)
	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"), map[string]any{
		"identifier": "waiting",
		"password":   "correct horse battery",
	}, nil), http.StatusUnauthorized)

	// 7) 非管理员不能访问审核列表。
	requireStatus(t, request(harness.Router, http.MethodPost,
		testsupport.URL("/registration-requests/query"), map[string]any{}, nil), http.StatusUnauthorized)
}
