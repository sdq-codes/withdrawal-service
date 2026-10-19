package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sdq-codes/withdrawal-service/database/migrations"
)

// Fixed width so stored timestamps sort as strings.
const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func Timestamp(t time.Time) string { return t.UTC().Format(TimeLayout) }

func NullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func Open(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		path = "withdrawals.db"
	}
	pragmas := "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	var dsn string
	if path == ":memory:" {
		// Each in-memory database gets its own name so tests stay isolated.
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return nil, err
		}
		dsn = "file:withdrawal-memory-" + hex.EncodeToString(suffix) + "?mode=memory&cache=shared&" + pragmas
	} else {
		dsn = "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + pragmas + "&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serialises writes; the conditional UPDATE is still the funds check.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type Migration struct {
	Version int
	Name    string
}

func Migrate(ctx context.Context, db *sql.DB) ([]Migration, error) {
	all, err := listMigrations()
	if err != nil {
		return nil, err
	}
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return nil, err
	}
	var applied []Migration
	for _, m := range all {
		if m.Version <= current {
			continue
		}
		body, err := fs.ReadFile(migrations.SQL, m.Name)
		if err != nil {
			return applied, err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", m.Name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.Version)); err != nil {
			tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, m)
	}
	return applied, nil
}

func Version(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

func listMigrations() ([]Migration, error) {
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]Migration, 0, len(names))
	for i, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version != i+1 {
			return nil, fmt.Errorf("migration %q must be named %03d_<description>.sql", name, i+1)
		}
		out = append(out, Migration{Version: version, Name: name})
	}
	return out, nil
}
