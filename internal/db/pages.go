package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PageMeta 是 pages 的元数据（不含 html / body_text）。
type PageMeta struct {
	ID        string
	FolderID  int64
	Filename  string
	Title     string
	Size      int64
	SHA256    string
	Sort      int64
	CreatedAt int64
	UpdatedAt int64
}

// Page 是完整课件，仅在课件域读取时使用。
type Page struct {
	PageMeta
	HTML     []byte
	BodyText string
}

// SearchHit 是搜索候选，带正文用于生成 snippet。
type SearchHit struct {
	PageMeta
	BodyText string
}

const pageMetaCols = `id, folder_id, filename, title, size, sha256, sort, created_at, updated_at`

func scanPageMeta(row interface{ Scan(...any) error }) (*PageMeta, error) {
	var p PageMeta
	err := row.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Title, &p.Size, &p.SHA256, &p.Sort, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) PageByID(ctx context.Context, q Querier, id string) (*PageMeta, error) {
	return scanPageMeta(q.QueryRowContext(ctx,
		`SELECT `+pageMetaCols+` FROM pages WHERE id = ?`, id))
}

func (s *Store) PageByFolderFilename(ctx context.Context, q Querier, folderID int64, filename string) (*PageMeta, error) {
	return scanPageMeta(q.QueryRowContext(ctx,
		`SELECT `+pageMetaCols+` FROM pages WHERE folder_id = ? AND filename = ?`, folderID, filename))
}

func (s *Store) PageByFolderSHA(ctx context.Context, q Querier, folderID int64, sha string) (*PageMeta, error) {
	return scanPageMeta(q.QueryRowContext(ctx,
		`SELECT `+pageMetaCols+` FROM pages WHERE folder_id = ? AND sha256 = ? ORDER BY created_at ASC LIMIT 1`, folderID, sha))
}

func (s *Store) PageContent(ctx context.Context, q Querier, folderID int64, filename string) (*Page, error) {
	var p Page
	err := q.QueryRowContext(ctx,
		`SELECT `+pageMetaCols+`, html, body_text FROM pages WHERE folder_id = ? AND filename = ?`,
		folderID, filename).
		Scan(&p.ID, &p.FolderID, &p.Filename, &p.Title, &p.Size, &p.SHA256, &p.Sort, &p.CreatedAt, &p.UpdatedAt, &p.HTML, &p.BodyText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) PagesByFolder(ctx context.Context, q Querier, folderID int64) ([]PageMeta, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+pageMetaCols+` FROM pages WHERE folder_id = ? ORDER BY sort ASC, created_at ASC`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PageMeta
	for rows.Next() {
		var p PageMeta
		if err := rows.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Title, &p.Size, &p.SHA256, &p.Sort, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SearchCandidates 执行文档中的 LIKE 查询，可见性过滤留给调用方。
func (s *Store) SearchCandidates(ctx context.Context, q Querier, pattern string, limit int) ([]SearchHit, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+pageMetaCols+`, body_text
		 FROM pages
		 WHERE title LIKE ? ESCAPE '\' OR body_text LIKE ? ESCAPE '\'
		 ORDER BY updated_at DESC
		 LIMIT ?`, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.FolderID, &h.Filename, &h.Title, &h.Size, &h.SHA256, &h.Sort, &h.CreatedAt, &h.UpdatedAt, &h.BodyText); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) PageCounts(ctx context.Context, q Querier) (map[int64]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT folder_id, COUNT(*) FROM pages GROUP BY folder_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) InsertPage(ctx context.Context, q Querier, p *Page) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO pages(id, folder_id, filename, title, html, body_text, size, sha256, sort, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.FolderID, p.Filename, p.Title, p.HTML, p.BodyText, p.Size, p.SHA256, p.Sort, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) UpdatePageContent(ctx context.Context, q Querier, id, title string, html []byte, body string, size int64, sha string, now int64) error {
	res, err := q.ExecContext(ctx,
		`UPDATE pages SET title = ?, html = ?, body_text = ?, size = ?, sha256 = ?, updated_at = ? WHERE id = ?`,
		title, html, body, size, sha, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdatePageMeta(ctx context.Context, q Querier, id string, folderID int64, title string, sort int64, now int64) error {
	res, err := q.ExecContext(ctx,
		`UPDATE pages SET folder_id = ?, title = ?, sort = ?, updated_at = ? WHERE id = ?`,
		folderID, title, sort, now, id)
	if err != nil {
		if IsUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePage(ctx context.Context, q Querier, id string) error {
	res, err := q.ExecContext(ctx, `DELETE FROM pages WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func NowUnix() int64 { return time.Now().Unix() }
