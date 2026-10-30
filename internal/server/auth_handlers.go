package server

import (
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, ok := s.auth.Authenticate(w, r); ok {
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		}
		s.serveHTML(w, "login.html")
	case http.MethodPost:
		s.handleLoginPost(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "跨站请求被拒绝")
		return
	}

	ip := clientIP(r)
	if ok, retry := s.auth.Limiter().Allow(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请 15 分钟后再试")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "表单格式错误")
		return
	}
	password := r.PostForm.Get("password")

	if !s.auth.CheckPassword(password) {
		s.auth.Limiter().Fail(ip)
		s.log.Warn("login failed", "ip", ip, "ua", r.UserAgent())
		http.Redirect(w, r, "/admin/login?e=1", http.StatusSeeOther)
		return
	}

	s.auth.Limiter().Success(ip)
	now := time.Now().Unix()
	_ = s.store.DeleteExpiredSessions(r.Context(), s.store.DB, now-int64(7*24*time.Hour/time.Second))

	token, err := s.auth.NewSession(r.Context(), ip, r.UserAgent())
	if err != nil {
		s.log.Error("创建会话失败", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		return
	}
	s.auth.SetCookie(w, token)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if sess := SessionFrom(r.Context()); sess != nil {
		_ = s.store.DeleteSession(r.Context(), s.store.DB, sess.ID)
	}
	s.auth.ClearCookie(w)
	writeNoContent(w)
}
