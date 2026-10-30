package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"homelab-teach/internal/auth"
	"homelab-teach/internal/config"
	"homelab-teach/internal/db"
)

type Server struct {
	cfg   *config.Config
	store *db.Store
	auth  *auth.Manager
	web   fs.FS
	log   *slog.Logger
}

func New(cfg *config.Config, store *db.Store, webFS fs.FS, logger *slog.Logger) *Server {
	return &Server{
		cfg:   cfg,
		store: store,
		auth:  auth.New(store, cfg),
		web:   webFS,
		log:   logger,
	}
}

type sessionKey struct{}

func SessionFrom(ctx context.Context) *db.Session {
	s, _ := ctx.Value(sessionKey{}).(*db.Session)
	return s
}

// Handler 组装中间件：recover -> 访问日志 -> guard -> route。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "stack", string(debug.Stack()))
				if sw.status == 0 {
					writeErr(sw, http.StatusInternalServerError, "internal", "服务器内部错误")
				}
			}
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			s.log.Info("request",
				"method", r.Method,
				"path", r.URL.RequestURI(),
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", clientIP(r),
			)
		}()
		s.guard(http.HandlerFunc(s.route)).ServeHTTP(sw, r)
	})
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			!strings.HasPrefix(r.URL.Path, "/admin/") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.setAppHeaders(w)
	p := r.URL.Path

	if p == "/admin" {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		return
	}
	if strings.HasPrefix(p, "/admin/") {
		if p == "/admin/login" {
			s.handleLogin(w, r)
			return
		}
		s.requireAdmin(s.adminRoute).ServeHTTP(w, r)
		return
	}
	s.publicRoute(w, r)
}

func (s *Server) adminRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/" || r.URL.Path == "/admin/index.html":
		s.serveHTML(w, "admin.html")
	case r.URL.Path == "/admin/logout":
		s.handleLogout(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/api/"):
		s.adminAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/preview/"):
		s.handlePreviewOpen(w, r)
	default:
		s.notFound(w, r)
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.auth.Authenticate(w, r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/admin/api/") {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			} else {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			}
			return
		}
		if !s.originOK(r) {
			writeErr(w, http.StatusForbidden, "forbidden", "跨站请求被拒绝")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	}
}

// originOK 判断写请求是否来自同源页面。
// 不要求 Origin 等于写死的域名：浏览器实际访问的 host 与 Origin 一致即可，
// 这样 localhost、IP、反代域名都不会被误杀；跨站页面的 Origin 仍然对不上。
func (s *Server) originOK(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	// 浏览器明确标注为同源发起时直接放行。这一层不依赖反代是否
	// 正确传递 Host，避免改域名/加反代后把正常登录误杀。
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}

	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	if originMatches(raw, effectiveHost(r), s.cfg.Origin) {
		return true
	}
	s.log.Warn("拒绝跨站请求",
		"origin", raw,
		"host", r.Host,
		"x_forwarded_host", r.Header.Get("X-Forwarded-Host"),
	)
	return false
}

func originMatches(raw, reqHost, siteOrigin string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	got := normalizeHost(u.Host)
	if got == normalizeHost(reqHost) {
		return true
	}
	site, err := url.Parse(siteOrigin)
	return err == nil && site.Host != "" && got == normalizeHost(site.Host)
}

func effectiveHost(r *http.Request) string {
	h := r.Header.Get("X-Forwarded-Host")
	if h == "" {
		return r.Host
	}
	if i := strings.IndexByte(h, ','); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return h
	}
	if port == "80" || port == "443" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func (s *Server) setAppHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Robots-Tag", "noindex")
}

func (s *Server) serveHTML(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(s.web, name)
	if err != nil {
		s.log.Error("读取静态页面失败", "file", name, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) publicRoute(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/":
		s.serveHTML(w, "index.html")
	case strings.HasPrefix(p, "/static/"):
		s.serveStatic(w, r)
	case p == "/api/tree":
		s.handlePublicTree(w, r)
	case p == "/api/pages":
		s.handlePublicPages(w, r)
	case p == "/api/search":
		s.handleSearch(w, r)
	case strings.HasPrefix(p, "/f/"):
		s.serveCourseware(w, r)
	case strings.HasPrefix(p, "/api/site/"):
		s.handlePublicSite(w, r)
	case p == "/healthz":
		s.handleHealthz(w, r)
	case strings.HasPrefix(p, "/api/"):
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
	default:
		s.notFound(w, r)
	}
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	types := map[string]string{
		"app.css":     "text/css; charset=utf-8",
		"app.js":      "text/javascript; charset=utf-8",
		"admin.js":    "text/javascript; charset=utf-8",
		"favicon.svg": "image/svg+xml",
	}
	ct, ok := types[name]
	if !ok || strings.Contains(name, "/") {
		s.notFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.web, name)
	if err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	data, err := fs.ReadFile(s.web, "404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(data)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	var one int
	if err := s.store.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("db unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
