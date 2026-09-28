package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionName = "download_session"

type loginPageData struct {
	Error string
	Next  string
}

func loadAdminFile(path string) (user, pass string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, "\"'")
		switch key {
		case "ADMIN_USER":
			if val != "" {
				user = val
			}
		case "ADMIN_PASS":
			pass = val
		}
	}
	return user, pass, nil
}

func loadSessionKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		key := make([]byte, 32)
		copy(key, b[:32])
		return key, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderLogin(w, http.StatusOK, "", r.URL.Query().Get("next"))
		return
	}
	ip := clientIP(r.RemoteAddr)
	if s.AdminPass == "" {
		s.renderLogin(w, http.StatusForbidden, "Admin login is not configured.", "")
		return
	}
	if s.lockedOut(ip) {
		s.renderLogin(w, http.StatusTooManyRequests, "Too many tries. Wait a minute and try again.", r.FormValue("next"))
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, http.StatusBadRequest, "Could not read the form.", "")
		return
	}
	user := r.PostForm.Get("username")
	pass := r.PostForm.Get("password")
	next := r.PostForm.Get("next")
	if !secretEqual(user, s.AdminUser) || !secretEqual(pass, s.AdminPass) {
		s.noteFailure(ip)
		s.renderLogin(w, http.StatusUnauthorized, "Wrong username or password.", next)
		return
	}
	s.clearFailures(ip)
	s.setSession(w, s.AdminUser)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

func safeNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.Contains(raw, "://") {
		return "/logs"
	}
	return raw
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, sessionCookie("", -1))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, msg, next string) {
	setHTML(w)
	w.WriteHeader(status)
	_ = loginPage.Execute(w, loginPageData{Error: msg, Next: safeNext(next)})
}

func (s *Server) loggedIn(r *http.Request) bool {
	if s.AdminPass == "" || len(s.SessionKey) == 0 {
		return false
	}
	c, err := r.Cookie(sessionName)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, s.mac(payload)) {
		return false
	}
	user, expText, ok := strings.Cut(string(payload), "|")
	if !ok || !secretEqual(user, s.AdminUser) {
		return false
	}
	exp, err := strconv.ParseInt(expText, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}

func (s *Server) setSession(w http.ResponseWriter, user string) {
	exp := time.Now().Add(24 * time.Hour).Unix()
	payload := []byte(user + "|" + strconv.FormatInt(exp, 10))
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload))
	http.SetCookie(w, sessionCookie(token, 24*60*60))
}

func (s *Server) mac(payload []byte) []byte {
	sum := hmac.New(sha256.New, s.SessionKey)
	sum.Write(payload)
	return sum.Sum(nil)
}

func secretEqual(got, want string) bool {
	if want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

type loginFail struct {
	count int
	until time.Time
}

var (
	loginMu    sync.Mutex
	loginFails = map[string]loginFail{}
)

func (s *Server) lockedOut(ip string) bool {
	loginMu.Lock()
	defer loginMu.Unlock()
	state, ok := loginFails[ip]
	return ok && time.Now().Before(state.until)
}

func (s *Server) noteFailure(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	loginMu.Lock()
	defer loginMu.Unlock()
	state := loginFails[ip]
	state.count++
	if state.count >= 8 {
		state.until = time.Now().Add(time.Minute)
		state.count = 0
	}
	loginFails[ip] = state
}

func (s *Server) clearFailures(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginFails, ip)
}

var loginPage = template.Must(template.New("login").Parse(
	"<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Admin login</title>\n" +
		"<style>\nbody { font-family: sans-serif; margin: 2rem; }\n" +
		"label { display: block; margin-top: 0.8rem; }\n" +
		"input { font: inherit; padding: 0.3rem; }\n" +
		"button { margin-top: 1rem; font: inherit; padding: 0.4rem 0.8rem; }\n" +
		".err { color: #9b1c1c; }\n</style>\n</head>\n<body>\n<h1>Admin login</h1>\n" +
		"<p>Downloads stay open. The log page is only for an admin.</p>\n" +
		"{{if .Error}}<p class=\"err\">{{.Error}}</p>{{end}}\n" +
		"<form method=\"post\" action=\"/login\">\n" +
		"<input type=\"hidden\" name=\"next\" value=\"{{.Next}}\">\n" +
		"<label>Username <input name=\"username\" autocomplete=\"username\"></label>\n" +
		"<label>Password <input name=\"password\" type=\"password\" autocomplete=\"current-password\"></label>\n" +
		"<button type=\"submit\">Log in</button>\n</form>\n" +
		"<p><a href=\"/\">status</a></p>\n</body>\n</html>\n",
))
