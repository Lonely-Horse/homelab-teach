package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"net/http"
	"time"

	"homelab-teach/internal/config"
	"homelab-teach/internal/db"
)

const (
	SessionTTL   = 7 * 24 * time.Hour
	RenewEvery   = 24 * time.Hour
	CookieName   = "sid"
	CookieMaxAge = 7 * 24 * 60 * 60
)

// Manager 负责密码校验、会话与 cookie。
type Manager struct {
	store  *db.Store
	salt   []byte
	hash   []byte
	iters  int
	secret []byte
	fp     string
	dev    bool
	limit  *Limiter
}

func New(store *db.Store, cfg *config.Config) *Manager {
	return &Manager{
		store:  store,
		salt:   cfg.AdminSalt,
		hash:   cfg.AdminHash,
		iters:  cfg.AdminIters,
		secret: cfg.PwdSecret,
		fp:     cfg.PasswordFingerprint(),
		dev:    cfg.DevMode,
		limit:  NewLimiter(),
	}
}

// CheckPassword 使用 PBKDF2-HMAC-SHA256 + 常量时间比较。
func (m *Manager) CheckPassword(password string) bool {
	derived, err := pbkdf2.Key(sha256.New, password, m.salt, m.iters, len(m.hash))
	if err != nil || len(derived) != len(m.hash) {
		return false
	}
	return subtle.ConstantTimeCompare(derived, m.hash) == 1
}

// NewSession 创建会话并返回 token。
func (m *Manager) NewSession(ctx context.Context, ip, ua string) (string, error) {
	token, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	err = m.store.CreateSession(ctx, m.store.DB, &db.Session{
		ID:        token,
		CreatedAt: now,
		RenewedAt: now,
		PwdFP:     m.fp,
		IP:        ip,
		UA:        ua,
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate 校验请求中的会话。任何异常路径都返回 false（fail closed）。
// 需要续期时同时更新 renewed_at 与 cookie。
func (m *Manager) Authenticate(w http.ResponseWriter, r *http.Request) (*db.Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	ctx := r.Context()
	sess, err := m.store.SessionByID(ctx, m.store.DB, c.Value)
	if err != nil {
		return nil, false
	}
	if sess.PwdFP != m.fp {
		_ = m.store.DeleteSession(ctx, m.store.DB, sess.ID)
		return nil, false
	}

	now := time.Now().Unix()
	if now-sess.RenewedAt >= int64(SessionTTL/time.Second) {
		_ = m.store.DeleteSession(ctx, m.store.DB, sess.ID)
		return nil, false
	}
	if now-sess.RenewedAt >= int64(RenewEvery/time.Second) {
		if err := m.store.TouchSession(ctx, m.store.DB, sess.ID, now); err != nil {
			return nil, false
		}
		sess.RenewedAt = now
		m.SetCookie(w, sess.ID)
	}
	return sess, true
}

func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   !m.dev,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   CookieMaxAge,
	})
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   !m.dev,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (m *Manager) Limiter() *Limiter { return m.limit }

func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

var ErrRateLimited = errors.New("rate limited")
