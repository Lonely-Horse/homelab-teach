package db

import (
	"context"
	"database/sql"
	"errors"
)

type SitePage struct {
	Slug      string
	Title     string
	Body      string
	UpdatedAt int64
}

func (s *Store) SitePage(ctx context.Context, q Querier, slug string) (*SitePage, error) {
	var p SitePage
	err := q.QueryRowContext(ctx,
		`SELECT slug, title, body, updated_at FROM site_pages WHERE slug = ?`, slug).
		Scan(&p.Slug, &p.Title, &p.Body, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) SaveSitePage(ctx context.Context, q Querier, slug, title, body string, now int64) (*SitePage, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE site_pages SET title = ?, body = ?, updated_at = ? WHERE slug = ?`,
		title, body, now, slug)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return &SitePage{Slug: slug, Title: title, Body: body, UpdatedAt: now}, nil
}
