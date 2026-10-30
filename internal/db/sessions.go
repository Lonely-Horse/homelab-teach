package db

import (
	"context"
	"database/sql"
	"errors"
)

type Session struct {
	ID        string
	CreatedAt int64
	RenewedAt int64
	PwdFP     string
	IP        string
	UA        string
}

func (s *Store) CreateSession(ctx context.Context, q Querier, sess *Session) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO sessions(id, created_at, renewed_at, pwd_fp, ip, ua) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.CreatedAt, sess.RenewedAt, sess.PwdFP, sess.IP, sess.UA)
	return err
}

func (s *Store) SessionByID(ctx context.Context, q Querier, id string) (*Session, error) {
	var sess Session
	err := q.QueryRowContext(ctx,
		`SELECT id, created_at, renewed_at, pwd_fp, ip, ua FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.CreatedAt, &sess.RenewedAt, &sess.PwdFP, &sess.IP, &sess.UA)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, q Querier, id string, now int64) error {
	res, err := q.ExecContext(ctx, `UPDATE sessions SET renewed_at = ? WHERE id = ?`, now, id)
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

func (s *Store) DeleteSession(ctx context.Context, q Querier, id string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, q Querier, cutoff int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM sessions WHERE renewed_at < ?`, cutoff)
	return err
}

func (s *Store) ListSessions(ctx context.Context, q Querier, cutoff int64) ([]Session, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, created_at, renewed_at, pwd_fp, ip, ua FROM sessions WHERE renewed_at >= ? ORDER BY renewed_at DESC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.CreatedAt, &sess.RenewedAt, &sess.PwdFP, &sess.IP, &sess.UA); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// SessionIDsByPrefix 用于后台只显示 8 位前缀时的踢人操作。
func (s *Store) SessionIDsByPrefix(ctx context.Context, q Querier, prefix string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id FROM sessions WHERE id LIKE ? ESCAPE '\' LIMIT 2`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSessionsExcept(ctx context.Context, q Querier, keepID string) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM sessions WHERE id <> ?`, keepID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
