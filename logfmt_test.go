package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatGMT9(t *testing.T) {
	got := formatGMT9(time.Date(2026, 9, 27, 0, 30, 0, 0, time.UTC))
	if got != "2026-09-27 09:30:00" {
		t.Fatalf("gmt9 %q", got)
	}
}

func TestOSInfo(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0", "Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", "macOS"},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36", "Linux"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36", "Android"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)", "iOS"},
		{"curl/8.5.0 (Windows NT 10.0; Win64; x64)", "Windows"},
		{"curl/8.5.0 (x86_64-pc-linux-gnu)", "Linux"},
		{"curl/8.5.0 (x86_64-apple-darwin23.0)", "macOS"},
		{"curl/8.5.0", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		if got := osInfo(tc.ua); got != tc.want {
			t.Fatalf("osInfo(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
}

func TestReadableLogEscapesNewlines(t *testing.T) {
	got := formatReadable(downloadRecord{
		Time: "2026-09-27 09:30:00",
		IP:   "203.0.113.4",
		File: "a\nb.txt",
		OS:   "Windows",
		PC:   "desk\rtop",
	})
	if strings.Contains(got, "\n\n\n") || strings.Count(got, "\n") != 6 {
		t.Fatalf("log lines %q", got)
	}
	if !strings.Contains(got, "File: a b.txt") || !strings.Contains(got, "PC name: desk top") {
		t.Fatalf("log %q", got)
	}
	if !strings.Contains(got, "Country, city (IP): 203.0.113.4") {
		t.Fatalf("log %q", got)
	}
}

func TestFormatLocation(t *testing.T) {
	if got := formatLocation("Seoul, South Korea", "203.0.113.4"); got != "Seoul, South Korea (203.0.113.4)" {
		t.Fatalf("place %q", got)
	}
	if got := formatLocation("", "203.0.113.4"); got != "203.0.113.4" {
		t.Fatalf("legacy %q", got)
	}
}

func TestLookupLocation(t *testing.T) {
	if got := lookupLocation("127.0.0.1"); got != "localhost" {
		t.Fatalf("loopback %q", got)
	}
	if got := lookupLocation("::1"); got != "localhost" {
		t.Fatalf("ipv6 loopback %q", got)
	}
	if got := lookupLocation("10.1.2.3"); got != "private" {
		t.Fatalf("private %q", got)
	}
	if got := lookupLocation("not-an-ip"); got != "unknown" {
		t.Fatalf("bad ip %q", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"status\":\"success\",\"country\":\"United States\",\"regionName\":\"Virginia\",\"city\":\"Ashburn\"}"))
	}))
	t.Cleanup(srv.Close)
	prev := geoEndpoint
	geoEndpoint = srv.URL + "/%s"
	t.Cleanup(func() { geoEndpoint = prev })
	locationMu.Lock()
	delete(locationCache, "203.0.113.9")
	locationMu.Unlock()

	if got := lookupLocation("203.0.113.9"); got != "United States, Ashburn" {
		t.Fatalf("lookup %q", got)
	}
	if got := lookupLocation("203.0.113.9"); got != "United States, Ashburn" {
		t.Fatalf("cached lookup %q", got)
	}
}

func TestLegacyDatabaseGetsLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec("CREATE TABLE downloads (" +
		"id INTEGER PRIMARY KEY AUTOINCREMENT, " +
		"downloaded_at TEXT NOT NULL, " +
		"ip TEXT NOT NULL, " +
		"file_name TEXT NOT NULL, " +
		"os_info TEXT NOT NULL, " +
		"pc_name TEXT NOT NULL)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec("INSERT INTO downloads (downloaded_at, ip, file_name, os_info, pc_name) VALUES ('2026-09-28 12:00:00', '203.0.113.8', 'old.txt', 'Windows', 'unknown')")
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := insertDownload(db, downloadRecord{
		Time: "2026-09-28 12:01:00", IP: "203.0.113.9", Location: "Seoul, South Korea",
		File: "new.txt", OS: "curl", PC: "localhost",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := listDownloads(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].LocationLine() != "Seoul, South Korea (203.0.113.9)" {
		t.Fatalf("new row %q", rows[0].LocationLine())
	}
	if rows[1].LocationLine() != "203.0.113.8" {
		t.Fatalf("old row %q", rows[1].LocationLine())
	}
}
