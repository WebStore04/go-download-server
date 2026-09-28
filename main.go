package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address (use :8080 to accept connections on a VPS)")
	dir := flag.String("dir", "files", "directory of files to download")
	logPath := flag.String("log", "downloads.log", "human-readable download log")
	dbPath := flag.String("db", "downloads.db", "sqlite database of finished downloads")
	projects := flag.String("projects", "/var/www/html/git-projects", "admin-only project files")
	adminUser := flag.String("admin-user", getenv("ADMIN_USER", "admin"), "admin username for /logs")
	adminPass := flag.String("admin-pass", os.Getenv("ADMIN_PASS"), "admin password for /logs")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *addr, *dir, *logPath, *dbPath, *projects, *adminUser, *adminPass); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr, dir, logPath, dbPath, projects, adminUser, adminPass string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("files dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	defer logFile.Close()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absProjects, err := filepath.Abs(projects)
	if err != nil {
		return err
	}
	absLog, err := filepath.Abs(logPath)
	if err != nil {
		return err
	}
	db, err := openDB(dbPath)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	stateDir := filepath.Dir(absDB)
	adminPath := filepath.Join(stateDir, "admin.env")
	fileUser, filePass, err := loadAdminFile(adminPath)
	if err != nil && adminPass == "" {
		return fmt.Errorf("admin file %s: %w", adminPath, err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin file %s: %v\n", adminPath, err)
	} else {
		if (adminUser == "" || adminUser == "admin") && fileUser != "" {
			adminUser = fileUser
		}
		if adminPass == "" {
			adminPass = filePass
		}
	}
	keyPath := filepath.Join(stateDir, "session.key")
	sessionKey, err := loadSessionKey(keyPath)
	if err != nil {
		return fmt.Errorf("session key: %w", err)
	}
	if adminPass == "" {
		fmt.Fprintf(os.Stderr, "admin password missing: write ADMIN_USER and ADMIN_PASS to %s\n", adminPath)
	} else {
		fmt.Printf("admin login user: %s\nadmin password: set\n", adminUser)
	}
	fmt.Printf("download server listening on %s\nfiles: %s\nprojects: %s\nlog: %s\ndatabase: %s\nlogin: /login\n%s\n", addr, absDir, absProjects, absLog, absDB, listenHint(addr))

	srv := &http.Server{
		Addr: addr,
		Handler: (&Server{
			Dir:        absDir,
			Projects:   absProjects,
			Log:        io.MultiWriter(os.Stdout, logFile),
			DB:         db,
			AdminUser:  adminUser,
			AdminPass:  adminPass,
			SessionKey: sessionKey,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func listenHint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		port := strings.TrimPrefix(addr, ":")
		return fmt.Sprintf("accepting connections on all interfaces, port %s. Downloads are public. Logs require http://YOUR_VPS_IP:%s/login", port, port)
	}
	return "try: curl -O http://" + addr + "/files/<name>"
}
