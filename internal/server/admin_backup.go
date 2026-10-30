package server

import (
	"net/http"
	"path/filepath"
	"time"
)

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	dataDir := filepath.Dir(s.cfg.DBPath)
	rel, size, err := s.store.Backup(r.Context(), dataDir)
	if err != nil {
		s.log.Error("备份失败", "err", err)
		writeErr(w, http.StatusInternalServerError, "backup_failed", "备份失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":       rel,
		"size":       size,
		"created_at": time.Now().Unix(),
	})
}
