package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"homelab-teach/internal/db"
)

type publicFolder struct {
	ID        int64  `json:"id"`
	ParentID  int64  `json:"parent_id"`
	Name      string `json:"name"`
	Sort      int64  `json:"sort"`
	PageCount int    `json:"page_count"`
}

type publicPage struct {
	FolderID  int64  `json:"folder_id"`
	Filename  string `json:"filename"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	UpdatedAt int64  `json:"updated_at"`
}

type searchItem struct {
	FolderID  int64  `json:"folder_id"`
	Filename  string `json:"filename"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *Server) handlePublicTree(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folders, err := s.store.Folders(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	counts, err := s.store.PageCounts(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "统计课件数失败", err)
		return
	}

	byID := make(map[int64]db.Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}

	out := make([]publicFolder, 0, len(folders))
	for _, f := range folders {
		if f.ID == 1 || !db.EffectivePublic(byID, f.ID) {
			continue
		}
		out = append(out, publicFolder{
			ID:        f.ID,
			ParentID:  f.ParentID,
			Name:      f.Name,
			Sort:      f.Sort,
			PageCount: counts[f.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": 1, "folders": out})
}

func (s *Server) handlePublicPages(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("folder_id")
	if idStr == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 folder_id")
		return
	}
	folderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "folder_id 必须是整数")
		return
	}

	ctx := r.Context()
	folders, err := s.store.Folders(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	byID := make(map[int64]db.Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	if _, ok := byID[folderID]; !ok || !db.EffectivePublic(byID, folderID) {
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}

	pages, err := s.store.PagesByFolder(ctx, s.store.DB, folderID)
	if err != nil {
		s.internal(w, "读取课件列表失败", err)
		return
	}
	items := make([]publicPage, 0, len(pages))
	for _, p := range pages {
		items = append(items, publicPage{
			FolderID:  p.FolderID,
			Filename:  p.Filename,
			Title:     p.Title,
			URL:       pageURL(p.FolderID, p.Filename),
			Size:      p.Size,
			UpdatedAt: p.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder_id": folderID, "items": items})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) > 100 {
		writeErr(w, http.StatusBadRequest, "bad_request", "关键词不能超过 100 字符")
		return
	}
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"query": "", "count": 0, "items": []searchItem{}})
		return
	}

	ctx := r.Context()
	hits, err := s.store.SearchCandidates(ctx, s.store.DB, "%"+escapeLike(q)+"%", 50)
	if err != nil {
		s.internal(w, "搜索失败", err)
		return
	}
	folders, err := s.store.Folders(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	byID := make(map[int64]db.Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}

	items := make([]searchItem, 0, len(hits))
	for _, h := range hits {
		if !db.EffectivePublic(byID, h.FolderID) {
			continue
		}
		items = append(items, searchItem{
			FolderID:  h.FolderID,
			Filename:  h.Filename,
			Title:     h.Title,
			URL:       pageURL(h.FolderID, h.Filename),
			Snippet:   makeSnippet(h.BodyText, q),
			UpdatedAt: h.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "count": len(items), "items": items})
}

func (s *Server) handlePublicSite(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/site/")
	if slug != "home" && slug != "about" {
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
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
		"slug":       p.Slug,
		"title":      p.Title,
		"body":       p.Body,
		"updated_at": p.UpdatedAt,
	})
}

func (s *Server) internal(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

func pageURL(folderID int64, filename string) string {
	return "/f/" + strconv.FormatInt(folderID, 10) + "/" + url.PathEscape(filename)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }

func makeSnippet(body, q string) string {
	runes := []rune(body)
	if len(runes) == 0 {
		return ""
	}
	qRunes := []rune(q)
	const radius = 40

	start, end := 0, 0
	byteIdx := strings.Index(strings.ToLower(body), strings.ToLower(q))
	if byteIdx >= 0 {
		hit := utf8.RuneCountInString(body[:byteIdx])
		start = hit - radius
		if start < 0 {
			start = 0
		}
		end = hit + len(qRunes) + radius
		if end > len(runes) {
			end = len(runes)
		}
	} else {
		end = 80
		if end > len(runes) {
			end = len(runes)
		}
	}

	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(string(runes[start:end]))
	if end < len(runes) {
		b.WriteString("…")
	}
	return b.String()
}
