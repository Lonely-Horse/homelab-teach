package server

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) adminAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/api/")
	switch {
	case path == "tree":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleAdminTree(w, r)
	case path == "folders":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleCreateFolder(w, r)
	case strings.HasPrefix(path, "folders/"):
		id, ok := pathID(path, "folders/")
		if !ok {
			writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
			return
		}
		switch r.Method {
		case http.MethodPatch:
			s.handlePatchFolder(w, r, id)
		case http.MethodDelete:
			s.handleDeleteFolder(w, r, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case path == "pages":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.handleAdminListPages(w, r)
		case http.MethodPost:
			s.handleUploadPage(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case strings.HasPrefix(path, "pages/"):
		rest := strings.TrimPrefix(path, "pages/")
		if strings.HasSuffix(rest, "/preview") {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(rest, "/preview")
			if id == "" || strings.Contains(id, "/") {
				writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
				return
			}
			s.handlePreviewPage(w, r, id)
			return
		}
		if rest == "" || strings.Contains(rest, "/") {
			writeErr(w, http.StatusNotFound, "page_not_found", "课件不存在")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.handleGetPage(w, r, rest)
		case http.MethodPatch:
			s.handlePatchPage(w, r, rest)
		case http.MethodDelete:
			s.handleDeletePage(w, r, rest)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case path == "import":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleImport(w, r)
	case strings.HasPrefix(path, "site/"):
		slug := strings.TrimPrefix(path, "site/")
		if slug != "home" && slug != "about" {
			writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.handleAdminGetSite(w, r, slug)
		case http.MethodPost:
			s.handleAdminSaveSite(w, r, slug)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case path == "backup":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleBackup(w, r)
	case path == "sessions":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.handleListSessions(w, r)
		case http.MethodDelete:
			s.handleDeleteOtherSessions(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case strings.HasPrefix(path, "sessions/"):
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(path, "sessions/")
		if id == "" || strings.Contains(id, "/") {
			writeErr(w, http.StatusNotFound, "session_not_found", "会话不存在")
			return
		}
		s.handleDeleteSession(w, r, id)
	default:
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
	}
}

func pathID(path, prefix string) (int64, bool) {
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
