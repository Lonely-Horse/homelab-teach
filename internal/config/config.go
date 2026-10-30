package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// 管理员凭证
//
// 由 `./homelab-teach -hash` 生成后粘贴到这里。
// 注意：salt/hash 会随源码进入版本库，安全性完全依赖密码强度与迭代次数。
// 要求：密码 >= 20 位随机串，或 iters >= 1,000,000。
// ---------------------------------------------------------------------------
const (
	adminSalt  = ""
	adminHash  = ""
	adminIters = 600000
	pwdSecret  = ""

	// 站点域名：部署前改这里，重新编译。
	// 单域名部署：课件与后台同源，课件里的 JS 能带会话调用 /admin/api/*，
	// 只应上传自己信任的 HTML。
	siteOrigin = "https://example.com"
)

// Config 是进程启动所需的全部配置。
type Config struct {
	Addr    string // 监听地址，默认 127.0.0.1:8080
	Origin  string // 站点地址，例如 https://teach.lonelyhorse.top
	DBPath  string // SQLite 文件路径
	DevMode bool   // 开发模式：cookie 不设 Secure

	AdminSaltB64 string
	AdminHashB64 string
	AdminSalt    []byte
	AdminHash    []byte
	AdminIters   int
	PwdSecret    []byte
}

// Load 校验 flag 参数并解码凭证常量。
func Load(addr, dbPath string, dev bool) (*Config, error) {
	if dbPath == "" {
		return nil, errors.New("-db 不能为空")
	}
	cfg := &Config{
		Addr:    addr,
		DBPath:  dbPath,
		DevMode: dev,

		AdminSaltB64: adminSalt,
		AdminHashB64: adminHash,
		AdminIters:   adminIters,
	}

	if dev {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("-addr 需要 host:port: %w", err)
		}
		cfg.Origin = "http://localhost:" + port
	} else {
		cfg.Origin = siteOrigin
	}
	if err := validateOrigin("siteOrigin", cfg.Origin); err != nil {
		return nil, err
	}
	if cfg.AdminIters <= 0 {
		return nil, errors.New("adminIters 必须大于 0")
	}

	if adminSalt == "" || adminHash == "" || pwdSecret == "" {
		return nil, errors.New("管理员凭证未配置：先运行 `./homelab-teach -hash` 生成常量，填入 internal/config/config.go 后重新编译")
	}

	salt, err := base64.StdEncoding.DecodeString(adminSalt)
	if err != nil {
		return nil, fmt.Errorf("adminSalt 不是合法 base64: %w", err)
	}
	hash, err := base64.StdEncoding.DecodeString(adminHash)
	if err != nil {
		return nil, fmt.Errorf("adminHash 不是合法 base64: %w", err)
	}
	secret, err := base64.StdEncoding.DecodeString(pwdSecret)
	if err != nil {
		return nil, fmt.Errorf("pwdSecret 不是合法 base64: %w", err)
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("pwdSecret 必须解码为 32 字节，实际 %d 字节", len(secret))
	}

	cfg.AdminSalt = salt
	cfg.AdminHash = hash
	cfg.PwdSecret = secret
	return cfg, nil
}

// PasswordFingerprint 按文档公式计算密码指纹：
// hex(sha256(adminSalt || adminHash))[:16]，其中两个操作数是源码里的 base64 字符串。
func (c *Config) PasswordFingerprint() string {
	sum := sha256.Sum256([]byte(c.AdminSaltB64 + c.AdminHashB64))
	return hex.EncodeToString(sum[:])[:16]
}

// GenerateCredentials 生成一组可直接粘贴进 config.go 的常量。
func GenerateCredentials(password string, iters int) (saltB64, hashB64, secretB64 string, err error) {
	if password == "" {
		return "", "", "", errors.New("密码不能为空")
	}
	if iters <= 0 {
		iters = 600000
	}
	salt := make([]byte, 32)
	if _, err = rand.Read(salt); err != nil {
		return "", "", "", err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, iters, 32)
	if err != nil {
		return "", "", "", err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return "", "", "", err
	}
	return base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(hash),
		base64.StdEncoding.EncodeToString(secret),
		nil
}

// CredentialBlock 返回可粘贴的 Go 常量块。
func CredentialBlock(saltB64, hashB64, secretB64 string, iters int) string {
	var b strings.Builder
	b.WriteString("const (\n")
	b.WriteString("\tadminSalt  = " + strconv.Quote(saltB64) + "\n")
	b.WriteString("\tadminHash  = " + strconv.Quote(hashB64) + "\n")
	b.WriteString("\tadminIters = " + strconv.Itoa(iters) + "\n")
	b.WriteString("\tpwdSecret  = " + strconv.Quote(secretB64) + "\n")
	b.WriteString(")\n")
	return b.String()
}

func validateOrigin(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s 不是合法 URL: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s 必须是 http/https，当前 %q", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%s 缺少主机名", name)
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("%s 只能包含 scheme://host，不能带路径", name)
	}
	return nil
}
