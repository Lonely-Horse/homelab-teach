package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"homelab-teach/internal/db"
)

func (s *Server) handleAdminGetSite(w http.ResponseWriter, r *http.Request, slug string) {
	p, err := s.store.SitePage(r.Context(), s.store.DB, slug)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if err != nil {
		s.internal(w, "读取站点页面失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": p.Slug, "title": p.Title, "body": p.Body, "updated_at": p.UpdatedAt,
	})
}

func (s *Server) handleAdminSaveSite(w http.ResponseWriter, r *http.Request, slug string) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "JSON 格式错误")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || utf8.RuneCountInString(title) > 100 {
		writeErr(w, http.StatusBadRequest, "bad_request", "标题必须为 1–100 字符")
		return
	}
	if len(req.Body) > 100*1024 {
		writeErr(w, http.StatusBadRequest, "bad_request", "正文不能超过 100KB")
		return
	}
	p, err := s.store.SaveSitePage(r.Context(), s.store.DB, slug, title, req.Body, db.NowUnix())
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if err != nil {
		s.internal(w, "保存站点页面失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": p.Slug, "title": p.Title, "body": p.Body, "updated_at": p.UpdatedAt,
	})
}
