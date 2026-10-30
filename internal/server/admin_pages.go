package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"homelab-teach/internal/auth"
	"homelab-teach/internal/content"
	"homelab-teach/internal/db"
)

const maxPageBytes = 8 << 20

type preparedPage struct {
	Title string
	Body  string
	SHA   string
	Size  int64
}

func preparePage(filename string, raw []byte) (*preparedPage, string) {
	if len(raw) > maxPageBytes {
		return nil, "too_large"
	}
	if !utf8.Valid(raw) {
		return nil, "invalid_encoding"
	}
	if !content.ValidHTML(raw) {
		return nil, "invalid_html"
	}
	if !validFilename(filename) {
		return nil, "invalid_filename"
	}
	sum := sha256.Sum256(raw)
	return &preparedPage{
		Title: content.Title(raw, filename),
		Body:  content.BodyText(raw),
		SHA:   hex.EncodeToString(sum[:]),
		Size:  int64(len(raw)),
	}, ""
}

func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPageBytes+1<<20)
	if err := r.ParseMultipartForm(maxPageBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "单个课件不能超过 8MB")
			return
		}
		writeErr(w, http.StatusBadRequest, "bad_request", "multipart 格式错误")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	folderID, err := parseFormInt(r.FormValue("folder_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "folder_id 必须是整数")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 file 字段")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxPageBytes+1))
	if err != nil {
		s.internal(w, "读取上传文件失败", err)
		return
	}
	if len(raw) > maxPageBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "单个课件不能超过 8MB")
		return
	}

	ctx := r.Context()
	if _, err := s.store.Folder(ctx, s.store.DB, folderID); errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "folder_not_found", "目标文件夹不存在")
		return
	} else if err != nil {
		s.internal(w, "读取目标文件夹失败", err)
		return
	}

	prep, code := preparePage(hdr.Filename, raw)
	if code != "" {
		s.writePrepareError(w, code)
		return
	}

	status, meta, err := s.ingestPage(ctx, s.store.DB, folderID, hdr.Filename, raw, prep, 0)
	if err != nil {
		s.internal(w, "写入课件失败", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":     status,
		"id":         meta.ID,
		"folder_id":  meta.FolderID,
		"filename":   meta.Filename,
		"title":      meta.Title,
		"size":       meta.Size,
		"sort":       meta.Sort,
		"created_at": meta.CreatedAt,
		"updated_at": meta.UpdatedAt,
	})
}

func (s *Server) handleAdminListPages(w http.ResponseWriter, r *http.Request) {
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
	if _, err := s.store.Folder(ctx, s.store.DB, folderID); errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
		return
	} else if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	pages, err := s.store.PagesByFolder(ctx, s.store.DB, folderID)
	if err != nil {
		s.internal(w, "读取课件列表失败", err)
		return
	}
	items := make([]map[string]any, 0, len(pages))
	for i := range pages {
		items = append(items, pageMetaJSON(&pages[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder_id": folderID, "items": items})
}

func (s *Server) handleGetPage(w http.ResponseWriter, r *http.Request, id string) {
	p, err := s.store.PageByID(r.Context(), s.store.DB, id)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	if err != nil {
		s.internal(w, "读取课件失败", err)
		return
	}
	writeJSON(w, http.StatusOK, pageMetaJSON(p))
}

func (s *Server) handlePatchPage(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		FolderID *int64  `json:"folder_id"`
		Title    *string `json:"title"`
		Sort     *int64  `json:"sort"`
		Filename *string `json:"filename"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "JSON 格式错误")
		return
	}
	if req.Filename != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "filename 不可修改")
		return
	}

	ctx := r.Context()
	p, err := s.store.PageByID(ctx, s.store.DB, id)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	if err != nil {
		s.internal(w, "读取课件失败", err)
		return
	}

	changed := false
	folderID := p.FolderID
	title := p.Title
	sort := p.Sort

	if req.FolderID != nil && *req.FolderID != p.FolderID {
		target := *req.FolderID
		if _, err := s.store.Folder(ctx, s.store.DB, target); errors.Is(err, db.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "folder_not_found", "目标文件夹不存在")
			return
		} else if err != nil {
			s.internal(w, "读取目标文件夹失败", err)
			return
		}
		other, err := s.store.PageByFolderFilename(ctx, s.store.DB, target, p.Filename)
		if err == nil && other.ID != p.ID {
			writeErr(w, http.StatusConflict, "filename_conflict", "目标文件夹已有同名课件")
			return
		}
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			s.internal(w, "检查同名课件失败", err)
			return
		}
		folderID = target
		changed = true
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || utf8.RuneCountInString(t) > 200 {
			writeErr(w, http.StatusBadRequest, "bad_request", "标题必须为 1–200 字符")
			return
		}
		if t != p.Title {
			title = t
			changed = true
		}
	}
	if req.Sort != nil && *req.Sort != p.Sort {
		sort = *req.Sort
		changed = true
	}

	if changed {
		now := time.Now().Unix()
		if err := s.store.UpdatePageMeta(ctx, s.store.DB, p.ID, folderID, title, sort, now); err != nil {
			if errors.Is(err, db.ErrConflict) {
				writeErr(w, http.StatusConflict, "filename_conflict", "目标文件夹已有同名课件")
				return
			}
			s.internal(w, "更新课件失败", err)
			return
		}
		p.FolderID, p.Title, p.Sort = folderID, title, sort
		p.UpdatedAt = now
	}
	writeJSON(w, http.StatusOK, pageMetaJSON(p))
}

func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request, id string) {
	err := s.store.DeletePage(r.Context(), s.store.DB, id)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	if err != nil {
		s.internal(w, "删除课件失败", err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handlePreviewPage(w http.ResponseWriter, r *http.Request, id string) {
	p, err := s.store.PageByID(r.Context(), s.store.DB, id)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	if err != nil {
		s.internal(w, "读取课件失败", err)
		return
	}
	link, exp := s.previewURL(p)
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "expires_at": exp})
}

// handlePreviewOpen 供后台的预览链接直接跳转，避免 window.open 被拦截。
func (s *Server) handlePreviewOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/preview/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	p, err := s.store.PageByID(r.Context(), s.store.DB, id)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
		return
	}
	if err != nil {
		s.internal(w, "读取课件失败", err)
		return
	}
	link, _ := s.previewURL(p)
	http.Redirect(w, r, link, http.StatusSeeOther)
}

// previewURL 生成 5 分钟有效的签名路径。
func (s *Server) previewURL(p *db.PageMeta) (string, int64) {
	exp := time.Now().Add(5 * time.Minute).Unix()
	link := pageURL(p.FolderID, p.Filename) +
		"?e=" + strconv.FormatInt(exp, 10) + "&t=" + s.sign(p.FolderID, p.Filename, exp)
	return link, exp
}

func (s *Server) ingestPage(ctx context.Context, q db.Querier, folderID int64, filename string, raw []byte, prep *preparedPage, sort int64) (string, *db.PageMeta, error) {
	existing, err := s.store.PageByFolderFilename(ctx, q, folderID, filename)
	if err == nil {
		if existing.SHA256 == prep.SHA {
			return "skipped", existing, nil
		}
		now := time.Now().Unix()
		if err := s.store.UpdatePageContent(ctx, q, existing.ID, prep.Title, raw, prep.Body, prep.Size, prep.SHA, now); err != nil {
			return "", nil, err
		}
		existing.Title = prep.Title
		existing.Size = prep.Size
		existing.SHA256 = prep.SHA
		existing.UpdatedAt = now
		return "updated", existing, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return "", nil, err
	}

	same, err := s.store.PageByFolderSHA(ctx, q, folderID, prep.SHA)
	if err == nil {
		return "skipped", same, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return "", nil, err
	}

	id, err := auth.RandomToken(32)
	if err != nil {
		return "", nil, err
	}
	now := time.Now().Unix()
	p := &db.Page{
		PageMeta: db.PageMeta{
			ID:        id,
			FolderID:  folderID,
			Filename:  filename,
			Title:     prep.Title,
			Size:      prep.Size,
			SHA256:    prep.SHA,
			Sort:      sort,
			CreatedAt: now,
			UpdatedAt: now,
		},
		HTML:     raw,
		BodyText: prep.Body,
	}
	if err := s.store.InsertPage(ctx, q, p); err != nil {
		return "", nil, err
	}
	return "created", &p.PageMeta, nil
}

func (s *Server) writePrepareError(w http.ResponseWriter, code string) {
	switch code {
	case "too_large":
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "单个课件不能超过 8MB")
	case "invalid_encoding":
		writeErr(w, http.StatusBadRequest, "invalid_encoding", "文件不是合法 UTF-8")
	case "invalid_html":
		writeErr(w, http.StatusBadRequest, "invalid_html", "文件不含 <html 或 <!doctype")
	case "invalid_filename":
		writeErr(w, http.StatusBadRequest, "invalid_filename", "文件名必须为 .html/.htm，≤128 字符且不含 / \\ ..")
	default:
		writeErr(w, http.StatusBadRequest, "bad_request", "文件校验失败")
	}
}

func validFilename(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") || strings.ContainsRune(name, 0) {
		return false
	}
	return content.IsHTMLFilename(name)
}

func pageMetaJSON(p *db.PageMeta) map[string]any {
	return map[string]any{
		"id":         p.ID,
		"folder_id":  p.FolderID,
		"filename":   p.Filename,
		"title":      p.Title,
		"size":       p.Size,
		"sha256":     p.SHA256,
		"sort":       p.Sort,
		"created_at": p.CreatedAt,
		"updated_at": p.UpdatedAt,
	}
}

func parseFormInt(raw string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
}
