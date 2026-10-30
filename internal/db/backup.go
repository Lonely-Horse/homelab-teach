package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Backup 用 VACUUM INTO 生成一致性快照，返回相对于数据目录的文件名与大小。
func (s *Store) Backup(ctx context.Context, dataDir string) (rel string, size int64, err error) {
	dir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, err
	}

	base := "app-" + time.Now().Format("2006-01-02-150405") + ".db"
	path := filepath.Join(dir, base)
	for i := 1; ; i++ {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("app-%s-%d.db", time.Now().Format("2006-01-02-150405"), i))
	}

	// VACUUM INTO 的目标是服务端拼出的路径，不来自用户输入。
	quoted := strings.ReplaceAll(path, "'", "''")
	if _, err := s.DB.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return "", 0, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return filepath.Join("backups", filepath.Base(path)), info.Size(), nil
}
