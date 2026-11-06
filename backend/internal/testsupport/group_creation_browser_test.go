package testsupport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupCreationBrowser(t *testing.T) {
	if os.Getenv("GROUP_CREATION_BROWSER") != "1" {
		t.Skip("set GROUP_CREATION_BROWSER=1 for real browser integration")
	}
	h := NewHarness(t)
	applicant := h.RegisterAndLogin(t, "p05_applicant")
	other := h.RegisterAndLogin(t, "p05_other")
	manager := h.RegisterAndLogin(t, "p05_manager")
	h.CreateGroup(t, manager, "既有管理组")
	admin := h.RegisterAndLogin(t, "p05_admin")
	promoteToAdmin(t, h, admin.UserID)
	frontend, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Router.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(frontend, "dist", filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			path = filepath.Join(frontend, "dist", "index.html")
		}
		http.ServeFile(w, r, path)
	}))
	defer server.Close()
	cmd := exec.Command("node", "scripts/group-creation-browser.mjs", server.URL)
	cmd.Dir = frontend
	cmd.Env = append(os.Environ(), "P05_COOKIE_NAME="+h.Config.Security.SessionCookieName, "P05_APPLICANT="+applicant.CookieValue, "P05_OTHER="+other.CookieValue, "P05_MANAGER="+manager.CookieValue, "P05_ADMIN="+admin.CookieValue)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
