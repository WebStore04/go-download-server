package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errInvalidPath = errors.New("invalid path")

// Server serves files from Dir and records each finished download.
type Server struct {
	Dir        string
	Projects   string
	Log        io.Writer
	DB         *sql.DB
	PCName     func(ip string) string
	Locate     func(ip string) string
	AdminUser  string
	AdminPass  string
	SessionKey []byte

	mu sync.Mutex
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /{$}", s.help)
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /logs", s.logs)
	mux.HandleFunc("GET /projects", s.projectList)
	mux.HandleFunc("GET /projects/{path...}", s.projectDownload)
	mux.HandleFunc("GET /files", s.list)
	mux.HandleFunc("GET /files/{path...}", s.download)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func setHTML(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}

func (s *Server) help(w http.ResponseWriter, r *http.Request) {
	setHTML(w)
	_, _ = io.WriteString(w, "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>go is on the service</title>\n</head>\n<body>\n<p>go is on the service</p>\n</body>\n</html>\n")
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	names, err := listFiles(s.Dir)
	if err != nil {
		http.Error(w, "cannot list files", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string][]string{"files": names}); err != nil {
		return
	}
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	s.serveAndLog(w, r, s.Dir, r.PathValue("path"))
}

func (s *Server) projectDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	s.serveAndLog(w, r, s.Projects, r.PathValue("path"))
}

func (s *Server) serveAndLog(w http.ResponseWriter, r *http.Request, root, name string) {
	rec := &statusRecorder{ResponseWriter: w}
	s.serveFile(rec, r, root, name)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK && status != http.StatusPartialContent {
		return
	}
	if err := s.writeLog(r, name); err != nil {
		fmt.Fprintf(os.Stderr, "log: %v\n", err)
	}
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, root, name string) {
	if root == "" {
		http.NotFound(w, r)
		return
	}
	full, err := resolvePath(root, name)
	if err != nil {
		http.Error(w, "invalid file path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "cannot open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.NotFound(w, r)
		return
	}

	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": outputName(name),
	})
	r.Header.Del("If-Modified-Since")
	r.Header.Del("If-None-Match")
	r.Header.Del("If-Range")
	r.Header.Del("Range")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", disposition)
	http.ServeContent(w, r, info.Name(), time.Time{}, f)
}

func (s *Server) pcName(ip string) string {
	if s.PCName != nil {
		return s.PCName(ip)
	}
	return lookupPCName(ip)
}

func (s *Server) place(ip string) string {
	if s.Locate != nil {
		return s.Locate(ip)
	}
	return lookupLocation(ip)
}

func (s *Server) writeLog(r *http.Request, name string) error {
	ip := clientIP(r.RemoteAddr)
	rec := downloadRecord{
		Time:     formatGMT9(time.Now()),
		IP:       ip,
		Location: s.place(ip),
		File:     name,
		OS:       osInfo(r.UserAgent()),
		PC:       s.pcName(ip),
	}
	if s.DB != nil {
		if err := insertDownload(s.DB, rec); err != nil {
			return err
		}
	}
	if s.Log == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.Log, formatReadable(rec))
	return err
}

func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if s.loggedIn(r) {
		return true
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	return false
}

type projectPageData struct {
	Files []string
	Error string
}

func (s *Server) projectList(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	data := projectPageData{}
	if s.Projects == "" {
		data.Error = "The projects folder is not configured."
		setHTML(w)
		_ = projectsPage.Execute(w, data)
		return
	}
	names, err := listFiles(s.Projects)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			data.Error = "The projects folder is not available."
		} else {
			data.Error = "Cannot read the projects folder."
		}
	} else {
		data.Files = names
	}
	setHTML(w)
	if err := projectsPage.Execute(w, data); err != nil {
		return
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w, r) {
		return
	}
	var rows []downloadRecord
	if s.DB != nil {
		var err error
		rows, err = listDownloads(s.DB)
		if err != nil {
			http.Error(w, "cannot read logs", http.StatusInternalServerError)
			return
		}
	}
	setHTML(w)
	if err := logsPage.Execute(w, rows); err != nil {
		return
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

func outputName(name string) string {
	base := path.Base(path.Clean("/" + name))
	if base == "." || base == "/" || base == ".." {
		return "download"
	}
	return base
}

func resolvePath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\x00") || strings.Contains(name, "\\") {
		return "", errInvalidPath
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", errInvalidPath
		}
	}
	clean := path.Clean("/" + name)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errInvalidPath
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(rootAbs, filepath.FromSlash(clean))
	rel, err := filepath.Rel(rootAbs, full)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errInvalidPath
	}

	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return full, nil
		}
		return "", err
	}
	resolvedRel, err := filepath.Rel(rootAbs, resolved)
	if err != nil || !filepath.IsLocal(resolvedRel) {
		return "", errInvalidPath
	}
	return resolved, nil
}

func listFiles(root string) ([]string, error) {
	names := []string{}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(rootAbs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, p)
		if err != nil {
			return err
		}
		if !filepath.IsLocal(rel) {
			return nil
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

var logsPage = template.Must(template.New("logs").Parse(
	"<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta http-equiv=\"refresh\" content=\"60\">\n<title>Download logs</title>\n" +
		"<style>\nbody { font-family: sans-serif; margin: 2rem; }\n" +
		".bar { display: flex; align-items: center; gap: 1rem; }\n" +
		".bar h1 { margin: 0; }\n.bar form { margin: 0; }\n" +
		"button { font: inherit; padding: 0.35rem 0.7rem; }\n" +
		"table { border-collapse: collapse; margin-top: 1rem; }\n" +
		"th, td { border: 1px solid #ccc; padding: 0.4rem 0.7rem; text-align: left; }\n" +
		"th { background: #f3f3f3; }\n</style>\n</head>\n<body>\n" +
		"<div class=\"bar\"><h1>Download logs</h1>\n" +
		"<form method=\"post\" action=\"/logout\"><button type=\"submit\">Log out</button></form></div>\n" +
		"<p><a href=\"/projects\">projects</a></p>\n" +
		"{{if not .}}<p>No finished downloads yet.</p>{{else}}\n<table>\n<thead>\n<tr>\n" +
		"<th>Time (GMT+9)</th><th>Country, city (IP)</th><th>File</th><th>Operating system</th><th>PC name</th>\n" +
		"</tr>\n</thead>\n<tbody>\n{{range .}}<tr><td>{{.Time}}</td><td>{{.LocationLine}}</td><td>{{.File}}</td><td>{{.OS}}</td><td>{{.PC}}</td></tr>\n{{end}}\n" +
		"</tbody>\n</table>\n{{end}}\n</body>\n</html>\n",
))

var projectsPage = template.Must(template.New("projects").Parse(
	"<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Projects</title>\n" +
		"<style>\nbody { font-family: sans-serif; margin: 2rem; }\n" +
		".bar { display: flex; align-items: center; gap: 1rem; }\n" +
		".bar h1 { margin: 0; }\n.bar form { margin: 0; }\n" +
		"button { font: inherit; padding: 0.35rem 0.7rem; }\n" +
		"li { margin: 0.35rem 0; }\n</style>\n</head>\n<body>\n" +
		"<div class=\"bar\"><h1>Projects</h1>\n" +
		"<form method=\"post\" action=\"/logout\"><button type=\"submit\">Log out</button></form></div>\n" +
		"<p><a href=\"/logs\">logs</a></p>\n" +
		"{{if .Error}}<p>{{.Error}}</p>{{else if not .Files}}<p>No project files yet.</p>{{else}}\n<ul>\n" +
		"{{range .Files}}<li><a href=\"/projects/{{.}}\">{{.}}</a></li>\n{{end}}\n</ul>\n{{end}}\n" +
		"</body>\n</html>\n",
))

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}
