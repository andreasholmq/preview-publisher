package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

type Preview struct {
	Slug         string    `json:"slug"`
	Title        string    `json:"title,omitempty"`
	PublicURL    string    `json:"public_url"`
	Protected    bool      `json:"protected"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	SizeBytes    int64     `json:"size_bytes"`
	FileCount    int       `json:"file_count"`
	PasswordHash string    `json:"-"`
}

type UpsertPreview struct {
	Slug         string
	Title        string
	PublicURL    string
	PasswordHash string
	SizeBytes    int64
	FileCount    int
}

type UpsertResult struct {
	Preview  Preview
	Created  bool
	Replaced bool
}

func Open(dataDir string) (*DB, error) {
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "preview-publisher.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`pragma journal_mode = wal; pragma busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func appendUsage(ctx context.Context, tx *sql.Tx, action string, when time.Time) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_outbox(event_id,occurred_at,action) VALUES (?,?,?)`, hex.EncodeToString(id[:]), when.Format(time.RFC3339Nano), action)
	return err
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists usage_outbox (
 seq integer primary key autoincrement,
 event_id text unique not null,
 occurred_at text not null,
 action text not null check(action in ('publish','delete','password_change'))
);
create table if not exists previews (
  slug text primary key,
  title text,
  public_url text not null,
  password_hash text,
  created_at text not null,
  updated_at text not null,
  size_bytes integer not null,
  file_count integer not null
);
`)
	return err
}

func (d *DB) List(ctx context.Context) ([]Preview, error) {
	rows, err := d.db.QueryContext(ctx, `
select slug, coalesce(title, ''), public_url, coalesce(password_hash, ''), created_at, updated_at, size_bytes, file_count
from previews
order by updated_at desc, created_at desc
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var previews []Preview
	for rows.Next() {
		p, err := scanPreview(rows)
		if err != nil {
			return nil, err
		}
		previews = append(previews, p)
	}
	return previews, rows.Err()
}

func (d *DB) Get(ctx context.Context, slug string) (Preview, bool, error) {
	row := d.db.QueryRowContext(ctx, `
select slug, coalesce(title, ''), public_url, coalesce(password_hash, ''), created_at, updated_at, size_bytes, file_count
from previews
where slug = ?
`, slug)
	p, err := scanPreview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Preview{}, false, nil
	}
	if err != nil {
		return Preview{}, false, err
	}
	return p, true, nil
}

func (d *DB) Upsert(ctx context.Context, input UpsertPreview) (UpsertResult, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return UpsertResult{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().UTC()
	var createdAtRaw string
	err = tx.QueryRowContext(ctx, `select created_at from previews where slug = ?`, input.Slug).Scan(&createdAtRaw)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		created = true
		createdAtRaw = now.Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, `
insert into previews (slug, title, public_url, password_hash, created_at, updated_at, size_bytes, file_count)
values (?, nullif(?, ''), ?, nullif(?, ''), ?, ?, ?, ?)
`, input.Slug, input.Title, input.PublicURL, input.PasswordHash, createdAtRaw, now.Format(time.RFC3339Nano), input.SizeBytes, input.FileCount)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `
update previews
set title = nullif(?, ''),
    public_url = ?,
    password_hash = nullif(?, ''),
    updated_at = ?,
    size_bytes = ?,
    file_count = ?
where slug = ?
`, input.Title, input.PublicURL, input.PasswordHash, now.Format(time.RFC3339Nano), input.SizeBytes, input.FileCount, input.Slug)
	}
	if err != nil {
		return UpsertResult{}, err
	}
	if err := appendUsage(ctx, tx, "publish", now); err != nil {
		return UpsertResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpsertResult{}, err
	}
	p, _, err := d.Get(ctx, input.Slug)
	if err != nil {
		return UpsertResult{}, err
	}
	return UpsertResult{Preview: p, Created: created, Replaced: !created}, nil
}

func (d *DB) Delete(ctx context.Context, slug string) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `delete from previews where slug = ?`, slug)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := appendUsage(ctx, tx, "delete", time.Now().UTC()); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *DB) SetPassword(ctx context.Context, slug, hash string) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
update previews
set password_hash = ?, updated_at = ?
where slug = ?
`, hash, time.Now().UTC().Format(time.RFC3339Nano), slug)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := appendUsage(ctx, tx, "password_change", time.Now().UTC()); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *DB) ClearPassword(ctx context.Context, slug string) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
update previews
set password_hash = null, updated_at = ?
where slug = ?
`, time.Now().UTC().Format(time.RFC3339Nano), slug)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := appendUsage(ctx, tx, "password_change", time.Now().UTC()); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPreview(s scanner) (Preview, error) {
	var p Preview
	var createdRaw, updatedRaw string
	if err := s.Scan(&p.Slug, &p.Title, &p.PublicURL, &p.PasswordHash, &createdRaw, &updatedRaw, &p.SizeBytes, &p.FileCount); err != nil {
		return Preview{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return Preview{}, err
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		return Preview{}, err
	}
	p.CreatedAt = created
	p.UpdatedAt = updated
	p.Protected = p.PasswordHash != ""
	return p, nil
}
