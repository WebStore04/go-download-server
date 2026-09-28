package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type downloadRecord struct {
	Time     string
	IP       string
	Location string
	File     string
	OS       string
	PC       string
}

func (r downloadRecord) LocationLine() string {
	return formatLocation(r.Location, r.IP)
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS downloads (" +
		"id INTEGER PRIMARY KEY AUTOINCREMENT, " +
		"downloaded_at TEXT NOT NULL, " +
		"ip TEXT NOT NULL, " +
		"file_name TEXT NOT NULL, " +
		"os_info TEXT NOT NULL, " +
		"pc_name TEXT NOT NULL, " +
		"location TEXT NOT NULL DEFAULT '')"); err != nil {
		db.Close()
		return nil, fmt.Errorf("create downloads table: %w", err)
	}
	if err := ensureLocationColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("location column: %w", err)
	}
	return db, nil
}

func ensureLocationColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(downloads)")
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, colType string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notnull, &def, &pk); err != nil {
			return err
		}
		if name == "location" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec("ALTER TABLE downloads ADD COLUMN location TEXT NOT NULL DEFAULT ''")
	return err
}

func insertDownload(db *sql.DB, rec downloadRecord) error {
	_, err := db.Exec(
		"INSERT INTO downloads (downloaded_at, ip, location, file_name, os_info, pc_name) VALUES (?, ?, ?, ?, ?, ?)",
		rec.Time, rec.IP, rec.Location, rec.File, rec.OS, rec.PC,
	)
	return err
}

func listDownloads(db *sql.DB) ([]downloadRecord, error) {
	rows, err := db.Query("SELECT downloaded_at, ip, location, file_name, os_info, pc_name FROM downloads ORDER BY id DESC LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []downloadRecord{}
	for rows.Next() {
		var rec downloadRecord
		if err := rows.Scan(&rec.Time, &rec.IP, &rec.Location, &rec.File, &rec.OS, &rec.PC); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
