package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShutdownStopsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "downloads.log")
	dbPath := filepath.Join(t.TempDir(), "downloads.db")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, addr, dir, logPath, dbPath, t.TempDir(), "admin", "secret")
	}()

	deadline := time.Now().Add(3 * time.Second)
	var resp *http.Response
	for {
		resp, err = http.Get("http://" + addr + "/health")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("health %d %q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after cancel")
	}

	if _, err := http.Get("http://" + addr + "/health"); err == nil {
		t.Fatal("listener still accepted a connection")
	}
}

func TestListenHint(t *testing.T) {
	got := listenHint(":8080")
	for _, part := range []string{"8080", "YOUR_VPS_IP", "/login"} {
		if !strings.Contains(got, part) {
			t.Fatalf("hint %q missing %q", got, part)
		}
	}
	got = listenHint("127.0.0.1:8080")
	for _, part := range []string{"curl -O", "127.0.0.1:8080"} {
		if !strings.Contains(got, part) {
			t.Fatalf("local hint %q missing %q", got, part)
		}
	}
}
