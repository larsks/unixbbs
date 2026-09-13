// Package store implements the user-service's SQLite-backed account
// database: the users table (callsign -> uid) and get-or-create lookup.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// User is a single row of the users table.
type User struct {
	UID       int64
	Callsign  string
	CreatedAt string
}

// Store wraps the users database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// applies the embedded schema. WAL mode is enabled so that read-only
// consumers (mail-service, querying bbs.db directly) can run concurrently
// with this process's writes.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	// SQLite only supports one writer at a time; the Go driver otherwise
	// tries to run more than one connection concurrently.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// GetOrCreate looks up callsign (case-insensitively) and returns its
// User row, creating a new row with a freshly-allocated uid if none
// exists yet. The returned bool reports whether a new row was created.
func (s *Store) GetOrCreate(ctx context.Context, callsign string) (User, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (callsign) VALUES (?) ON CONFLICT (callsign) DO NOTHING;`,
		callsign,
	)
	if err != nil {
		return User{}, false, fmt.Errorf("insert user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return User{}, false, fmt.Errorf("rows affected: %w", err)
	}
	created := n > 0

	var u User
	row := tx.QueryRowContext(ctx,
		`SELECT uid, callsign, created_at FROM users WHERE callsign = ?1 COLLATE NOCASE;`,
		callsign,
	)
	if err := row.Scan(&u.UID, &u.Callsign, &u.CreatedAt); err != nil {
		return User{}, false, fmt.Errorf("select user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, false, fmt.Errorf("commit transaction: %w", err)
	}

	return u, created, nil
}

// List returns every known user, ordered by uid.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT uid, callsign, created_at FROM users ORDER BY uid;`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.UID, &u.Callsign, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	return users, nil
}
