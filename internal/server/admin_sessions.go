package server

import (
	"errors"
	"net/http"
	"time"

	"homelab-teach/internal/auth"
	"homelab-teach/internal/db"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	cutoff := time.Now().Add(-auth.SessionTTL).Unix()
	sessions, err := s.store.ListSessions(r.Context(), s.store.DB, cutoff)
	if err != nil {
		s.internal(w, "读取会话失败", err)
		return
	}
	current := SessionFrom(r.Context())
	items := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		id := sess.ID
		if len(id) > 8 {
			id = id[:8]
		}
		items = append(items, map[string]any{
			"id":         id,
			"created_at": sess.CreatedAt,
			"renewed_at": sess.RenewedAt,
			"ip":         sess.IP,
			"ua":         sess.UA,
			"current":    current != nil && sess.ID == current.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	if len(id) >= 8 {
		if sess, err := s.store.SessionByID(ctx, s.store.DB, id); err == nil {
			if err := s.store.DeleteSession(ctx, s.store.DB, sess.ID); err != nil {
				s.internal(w, "删除会话失败", err)
				return
			}
			writeNoContent(w)
			return
		} else if !errors.Is(err, db.ErrNotFound) {
			s.internal(w, "读取会话失败", err)
			return
		}

		ids, err := s.store.SessionIDsByPrefix(ctx, s.store.DB, id)
		if err != nil {
			s.internal(w, "读取会话失败", err)
			return
		}
		if len(ids) == 1 {
			if err := s.store.DeleteSession(ctx, s.store.DB, ids[0]); err != nil {
				s.internal(w, "删除会话失败", err)
				return
			}
			writeNoContent(w)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "session_not_found", "会话不存在")
}

func (s *Server) handleDeleteOtherSessions(w http.ResponseWriter, r *http.Request) {
	current := SessionFrom(r.Context())
	if current == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	n, err := s.store.DeleteSessionsExcept(r.Context(), s.store.DB, current.ID)
	if err != nil {
		s.internal(w, "删除会话失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
