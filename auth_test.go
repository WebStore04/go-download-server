package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAdminFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.env")
	body := "ADMIN_USER=admin\nADMIN_PASS=correct-password\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	user, pass, err := loadAdminFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if user != "admin" || pass != "correct-password" {
		t.Fatalf("user %q pass %q", user, pass)
	}
	_, _, err = loadAdminFile(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLogsArePrivateUntilLogin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	h := testServer(t, dir, &log).Handler()

	fileReq := httptest.NewRequest(http.MethodGet, "/files/sample.txt", nil)
	fileReq.Header.Set("User-Agent", "curl/8.5.0")
	fileRec := httptest.NewRecorder()
	h.ServeHTTP(fileRec, fileReq)
	if fileRec.Code != http.StatusOK || fileRec.Body.String() != "hello" {
		t.Fatalf("public download %d %q", fileRec.Code, fileRec.Body.String())
	}

	logs := httptest.NewRecorder()
	h.ServeHTTP(logs, httptest.NewRequest(http.MethodGet, "/logs", nil))
	if logs.Code != http.StatusSeeOther || logs.Header().Get("Location") != "/login?next=%2Flogs" {
		t.Fatalf("logs without login: %d location %q", logs.Code, logs.Header().Get("Location"))
	}
	if strings.Contains(logs.Body.String(), "sample.txt") {
		t.Fatal("logs response included a download row before login")
	}

	bad := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=nope"))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(bad, badReq)
	if bad.Code != http.StatusUnauthorized || !strings.Contains(bad.Body.String(), "Wrong username or password.") {
		t.Fatalf("bad login %d %q", bad.Code, bad.Body.String())
	}

	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, authedRequest(t, h, http.MethodGet, "/logs"))
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "sample.txt") {
		t.Fatalf("admin logs %d %q", ok.Code, ok.Body.String())
	}
}
