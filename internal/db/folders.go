package db

import (
	"context"
	"database/sql"
	"errors"
)

// Folder 对应 folders 一行。IsPublic == nil 表示继承父级。
type Folder struct {
	ID       int64
	ParentID int64
	Name     string
	IsPublic *int
	Sort     int64
}

func (s *Store) Folders(ctx context.Context, q Querier) ([]Folder, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, parent_id, name, is_public, sort FROM folders ORDER BY sort ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Folder
	for rows.Next() {
		var f Folder
		var pub sql.NullInt64
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &pub, &f.Sort); err != nil {
			return nil, err
		}
		if pub.Valid {
			v := int(pub.Int64)
			f.IsPublic = &v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Folder(ctx context.Context, q Querier, id int64) (*Folder, error) {
	var f Folder
	var pub sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT id, parent_id, name, is_public, sort FROM folders WHERE id = ?`, id).
		Scan(&f.ID, &f.ParentID, &f.Name, &pub, &f.Sort)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if pub.Valid {
		v := int(pub.Int64)
		f.IsPublic = &v
	}
	return &f, nil
}

func (s *Store) CreateFolder(ctx context.Context, q Querier, parentID int64, name string) (*Folder, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		`INSERT INTO folders(parent_id, name, is_public, sort) VALUES (?, ?, NULL, 0) RETURNING id`,
		parentID, name).Scan(&id)
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &Folder{ID: id, ParentID: parentID, Name: name, IsPublic: nil, Sort: 0}, nil
}

func (s *Store) UpdateFolder(ctx context.Context, q Querier, f *Folder) error {
	_, err := q.ExecContext(ctx,
		`UPDATE folders SET parent_id = ?, name = ?, is_public = ?, sort = ? WHERE id = ?`,
		f.ParentID, f.Name, boolToIntPtr(f.IsPublic), f.Sort, f.ID)
	if err != nil {
		if IsUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (s *Store) DeleteFolder(ctx context.Context, q Querier, id int64) error {
	res, err := q.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id)
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

// DeleteFolderTree 删除文件夹及其全部后代（含课件）。
// 返回删除的文件夹数与课件数；按深度倒序删除，避免 parent_id 外键约束。
func (s *Store) DeleteFolderTree(ctx context.Context, id int64) (folders, pages int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	const tree = `WITH RECURSIVE tree(id, depth) AS (
		SELECT id, 0 FROM folders WHERE id = ?
		UNION ALL
		SELECT f.id, t.depth + 1
		FROM folders f JOIN tree t ON f.parent_id = t.id
		WHERE t.depth < 64
	)`
	rows, err := tx.QueryContext(ctx, tree+` SELECT id FROM tree ORDER BY depth DESC`, id)
	if err != nil {
		return 0, 0, err
	}
	var ids []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		ids = append(ids, fid)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(ids) == 0 {
		return 0, 0, ErrNotFound
	}

	for _, fid := range ids {
		res, err := tx.ExecContext(ctx, `DELETE FROM pages WHERE folder_id = ?`, fid)
		if err != nil {
			return 0, 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		pages += int(n)
	}
	for _, fid := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, fid); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(ids), pages, nil
}

// FolderItemCount 返回直接子文件夹数与直接课件数。
func (s *Store) FolderItemCount(ctx context.Context, q Querier, id int64) (folders, pages int, err error) {
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE parent_id = ?`, id).Scan(&folders); err != nil {
		return 0, 0, err
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE folder_id = ?`, id).Scan(&pages); err != nil {
		return 0, 0, err
	}
	return folders, pages, nil
}

// EffectivePublic 按文档的三态继承算法解析文件夹可见性。
func EffectivePublic(folders map[int64]Folder, id int64) bool {
	cur := id
	for depth := 0; depth < 64; depth++ {
		f, ok := folders[cur]
		if !ok {
			return false
		}
		if f.IsPublic != nil {
			return *f.IsPublic == 1
		}
		if cur == 1 {
			return false
		}
		cur = f.ParentID
	}
	return false
}
