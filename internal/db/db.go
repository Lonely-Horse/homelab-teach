package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Querier 让同一套 SQL 既能走 *sql.DB，也能走 *sql.Tx。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrNotEmpty = errors.New("not empty")
)

// Store 是数据库句柄。连接池固定为 1，串行化消除 SQLITE_BUSY。
type Store struct {
	DB *sql.DB
}

// Open 打开数据库，并按文档设置连接级 PRAGMA。
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return nil, err
	}

	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := u.Query()
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()

	sqlDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &Store{DB: sqlDB}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

const schemaVersion = 1

var schemaStatements = []string{
	`CREATE TABLE folders (
		id        INTEGER PRIMARY KEY,
		parent_id INTEGER NOT NULL REFERENCES folders(id),
		name      TEXT    NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
		is_public INTEGER CHECK(is_public IN (0, 1)),
		sort      INTEGER NOT NULL DEFAULT 0,
		UNIQUE(parent_id, name)
	)`,
	`INSERT INTO folders(id, parent_id, name, is_public) VALUES (1, 1, '_root', 0)`,
	`CREATE TABLE pages (
		id         TEXT    PRIMARY KEY,
		folder_id  INTEGER NOT NULL REFERENCES folders(id),
		filename   TEXT    NOT NULL,
		title      TEXT    NOT NULL,
		html       BLOB    NOT NULL,
		body_text  TEXT    NOT NULL,
		size       INTEGER NOT NULL,
		sha256     TEXT    NOT NULL,
		sort       INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_pages_folder_filename ON pages(folder_id, filename)`,
	`CREATE TABLE sessions (
		id         TEXT    PRIMARY KEY,
		created_at INTEGER NOT NULL,
		renewed_at INTEGER NOT NULL,
		pwd_fp     TEXT    NOT NULL,
		ip         TEXT    NOT NULL DEFAULT '',
		ua         TEXT    NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE site_pages (
		slug       TEXT    PRIMARY KEY,
		title      TEXT    NOT NULL,
		body       TEXT    NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	)`,
	`INSERT INTO site_pages(slug, title, body, updated_at) VALUES
		('home',  '首页', '这里存放我的学习课件。', 0),
		('about', '个人', '这里写自我介绍。', 0)`,
}

// Migrate 按 PRAGMA user_version 执行迁移。整个迁移在单个事务内完成。
func (s *Store) Migrate(ctx context.Context) error {
	var version int
	if err := s.DB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取 user_version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于程序支持的 %d，拒绝启动", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range schemaStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("执行迁移语句: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("写入 user_version: %w", err)
	}
	return tx.Commit()
}

// IsUniqueViolation 兼容 modernc/sqlite 的错误文本。
func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func boolToIntPtr(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
