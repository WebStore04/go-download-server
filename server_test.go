package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadLogsCurlCommand(t *testing.T) {
	dir := t.TempDir()
	body := []byte("hello from the download server\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "notes")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "a b.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	var log strings.Builder
	srv := testServer(t, dir, &log)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/files/sample.txt", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("User-Agent", "curl/8.5.0 (Windows NT 10.0; Win64; x64)")
	req.Header.Set("Accept", "*/*")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(body) {
		t.Fatalf("body %q", rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "sample.txt") {
		t.Fatalf("disposition %q", rec.Header().Get("Content-Disposition"))
	}

	got := log.String()
	for _, part := range []string{"Time (GMT+9):", "Country, city (IP): Test City (192.0.2.1)", "File: sample.txt", "Operating system: Windows", "PC name: test-pc"} {
		if !strings.Contains(got, part) {
			t.Fatalf("log %q missing %q", got, part)
		}
	}

	page := httptest.NewRecorder()
	h.ServeHTTP(page, authedRequest(t, h, http.MethodGet, "/logs"))
	if page.Code != http.StatusOK {
		t.Fatalf("logs status %d", page.Code)
	}
	pageBody := page.Body.String()
	for _, part := range []string{"Time (GMT+9)", "Country, city (IP)", "sample.txt", "Windows", "test-pc", "Test City (192.0.2.1)", "Download logs", "Log out", "http-equiv=\"refresh\" content=\"60\""} {
		if !strings.Contains(pageBody, part) {
			t.Fatalf("logs page %q missing %q", pageBody, part)
		}
	}
	if strings.Contains(pageBody, ">status<") {
		t.Fatalf("logs page still has a status link: %q", pageBody)
	}

	log.Reset()
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/files/notes/a%20b.txt", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "nested" {
		t.Fatalf("nested status %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(log.String(), "File: notes/a b.txt") || !strings.Contains(log.String(), "Operating system: Windows") {
		t.Fatalf("nested log %q", log.String())
	}
}

func TestRepeatDownloadIsLogged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	h := testServer(t, dir, &log).Handler()

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/files/sample.txt", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
		req.Header.Set("If-Modified-Since", "Mon, 01 Jan 2024 00:00:00 GMT")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
			t.Fatalf("download %d status %d body %q", i, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cache header %q", rec.Header().Get("Cache-Control"))
		}
	}
	if strings.Count(log.String(), "File: sample.txt") != 2 {
		t.Fatalf("expected two log records, got %q", log.String())
	}
}

func TestMissingFileIsLogged(t *testing.T) {
	var log strings.Builder
	h := testServer(t, t.TempDir(), &log).Handler()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/files/missing.txt", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("User-Agent", "curl/8.5.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if log.Len() != 0 {
		t.Fatalf("missing file should not be logged, got %q", log.String())
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, authedRequest(t, h, http.MethodGet, "/logs"))
	if !strings.Contains(page.Body.String(), "No finished downloads yet.") {
		t.Fatalf("logs page %q", page.Body.String())
	}
}

func TestRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	if _, err := resolvePath(dir, "../secret.txt"); !errors.Is(err, errInvalidPath) {
		t.Fatalf("resolve ../ : %v", err)
	}
	if _, err := resolvePath(dir, "foo/../../secret.txt"); !errors.Is(err, errInvalidPath) {
		t.Fatalf("resolve nested ../ : %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	h := testServer(t, dir, &log).Handler()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/files/linked.txt", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("symlink status %d body %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("symlink escape returned file contents")
	}
	if log.Len() != 0 {
		t.Fatalf("rejected path should not be logged, got %q", log.String())
	}
}

func TestListAndHealth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	h := testServer(t, dir, &log).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/files", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d", rec.Code)
	}
	var payload struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Files) != 1 || payload.Files[0] != "sample.txt" {
		t.Fatalf("files %#v", payload.Files)
	}
	if log.Len() != 0 {
		t.Fatalf("list should not write a download log, got %q", log.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("health %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go is on the service") {
		t.Fatalf("home %d %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "admin login") {
		t.Fatalf("home still links to admin login: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("home content type %q", rec.Header().Get("Content-Type"))
	}
}

func TestProjectsListRefreshesForAdmin(t *testing.T) {
	publicDir := t.TempDir()
	projects := t.TempDir()
	if err := os.Mkdir(filepath.Join(projects, "group"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "group", "app.txt"), []byte("project"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	srv := testServer(t, publicDir, &log)
	srv.Projects = projects
	h := srv.Handler()

	anon := httptest.NewRecorder()
	h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if anon.Code != http.StatusSeeOther || !strings.Contains(anon.Header().Get("Location"), "/login") {
		t.Fatalf("anon list %d %s", anon.Code, anon.Header().Get("Location"))
	}
	secret := httptest.NewRecorder()
	h.ServeHTTP(secret, httptest.NewRequest(http.MethodGet, "/projects/group/app.txt", nil))
	if secret.Code != http.StatusSeeOther || secret.Body.String() == "project" {
		t.Fatalf("anon download %d %q", secret.Code, secret.Body.String())
	}

	page := httptest.NewRecorder()
	h.ServeHTTP(page, authedRequest(t, h, http.MethodGet, "/projects"))
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("projects page %d cache %q", page.Code, page.Header().Get("Cache-Control"))
	}
	if !strings.Contains(page.Body.String(), `href="/projects/group/app.txt"`) {
		t.Fatalf("list %q", page.Body.String())
	}

	got := httptest.NewRecorder()
	h.ServeHTTP(got, authedRequest(t, h, http.MethodGet, "/projects/group/app.txt"))
	if got.Code != http.StatusOK || got.Body.String() != "project" {
		t.Fatalf("download %d %q", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Header().Get("Content-Disposition"), "app.txt") {
		t.Fatalf("disposition %q", got.Header().Get("Content-Disposition"))
	}

	if err := os.WriteFile(filepath.Join(projects, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := httptest.NewRecorder()
	h.ServeHTTP(again, authedRequest(t, h, http.MethodGet, "/projects"))
	if !strings.Contains(again.Body.String(), "new.txt") || !strings.Contains(again.Body.String(), "group/app.txt") {
		t.Fatalf("refresh %q", again.Body.String())
	}

	pub := httptest.NewRecorder()
	h.ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/files", nil))
	if strings.Contains(pub.Body.String(), "app.txt") || strings.Contains(pub.Body.String(), "new.txt") {
		t.Fatalf("public list included project files: %s", pub.Body.String())
	}
}

func testServer(t *testing.T, dir string, log io.Writer) *Server {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "downloads.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Server{
		Dir:        dir,
		Log:        log,
		DB:         db,
		PCName:     func(string) string { return "test-pc" },
		Locate:     func(string) string { return "Test City" },
		AdminUser:  "admin",
		AdminPass:  "secret",
		SessionKey: []byte("0123456789abcdef0123456789abcdef"),
	}
}

func authedRequest(t *testing.T, h http.Handler, method, target string) *http.Request {
	t.Helper()
	form := strings.NewReader("username=admin&password=secret")
	login := httptest.NewRequest(http.MethodPost, "/login", form)
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, login)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status %d body %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(method, target, nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}
