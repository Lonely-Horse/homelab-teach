package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"homelab-teach/internal/db"
)

func (s *Server) serveCourseware(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Robots-Tag", "noindex")

	if !strings.HasPrefix(r.URL.Path, "/f/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/f/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	folderID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	filename := parts[1]
	if !validFilename(filename) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ctx := r.Context()
	allowed := s.validSignature(r, folderID, filename)
	if !allowed {
		folders, err := s.store.Folders(ctx, s.store.DB)
		if err != nil {
			s.log.Error("读取文件夹失败", "err", err)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		byID := make(map[int64]db.Folder, len(folders))
		for _, f := range folders {
			byID[f.ID] = f
		}
		allowed = db.EffectivePublic(byID, folderID)
	}
	if !allowed {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	p, err := s.store.PageContent(ctx, s.store.DB, folderID, filename)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		s.log.Error("读取课件失败", "err", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(p.HTML)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(p.HTML)
}

func (s *Server) sign(folderID int64, filename string, exp int64) string {
	msg := fmt.Sprintf("%d/%s|%d", folderID, filename, exp)
	mac := hmac.New(sha256.New, s.cfg.PwdSecret)
	_, _ = mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSignature(r *http.Request, folderID int64, filename string) bool {
	eStr := r.URL.Query().Get("e")
	tStr := r.URL.Query().Get("t")
	if eStr == "" || tStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(eStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(tStr)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(s.sign(folderID, filename, exp))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
