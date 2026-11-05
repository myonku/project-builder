package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestClientCancellationDoesNotLogInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	started, finished := make(chan struct{}), make(chan struct{})
	router := gin.New()
	router.Use(AccessLogMiddleware(logger), ErrorHandler(logger))
	router.GET("/read", func(c *gin.Context) {
		defer close(finished)
		close(started)
		<-c.Request.Context().Done()
		Abort(c, fmt.Errorf("查询失败: %w", c.Request.Context().Err()))
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/read", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := server.Client().Do(req); done <- err }()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("预期客户端请求被取消")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端未收到取消")
	}
	// finished 在中间件之前关闭，等待服务器退出以确保日志写入完成。
	server.Close()
	if strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "status=499") {
		t.Fatalf("取消请求的日志不正确: %s", logs.String())
	}
}

func TestInternalCancellationStillReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	router := gin.New()
	router.Use(ErrorHandler(slog.New(slog.NewTextHandler(&logs, nil))))
	router.GET("/read", func(c *gin.Context) { Abort(c, fmt.Errorf("内部取消: %w", context.Canceled)) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/read", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("内部取消应保持 500 和错误日志: status=%d log=%s", w.Code, logs.String())
	}
}
