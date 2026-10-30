package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"homelab-teach/internal/config"
	"homelab-teach/internal/db"
	"homelab-teach/internal/server"
)

//go:embed web
var webFiles embed.FS

func main() {
	hashMode := flag.Bool("hash", false, "生成管理员凭证常量后退出")
	addr := flag.String("addr", "127.0.0.1:8080", "监听地址")
	dbPath := flag.String("db", "./data/app.db", "SQLite 文件路径")
	dev := flag.Bool("dev", false, "开发模式：cookie 不设 Secure，域名用 localhost")
	flag.Parse()

	if *hashMode {
		runHash()
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load(*addr, *dbPath, *dev)
	if err != nil {
		logger.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	if cfg.DevMode {
		logger.Warn("开发模式：cookie 未设置 Secure")
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	migrateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = store.Migrate(migrateCtx)
	cancel()
	if err != nil {
		logger.Error("数据库迁移失败", "err", err)
		os.Exit(1)
	}

	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		logger.Error("加载前端资源失败", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(cfg, store, webFS, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	if host, _, err := net.SplitHostPort(cfg.Addr); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			logger.Warn("正在监听所有网卡；请确认宿主机端口只发布到 127.0.0.1 或 Tailscale 地址")
		}
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("服务启动", "addr", cfg.Addr, "db", cfg.DBPath)
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("服务异常退出", "err", err)
			os.Exit(1)
		}
	case sig := <-sigCh:
		logger.Info("收到退出信号", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("优雅关闭失败", "err", err)
			_ = srv.Close()
		}
	}
}

func runHash() {
	fmt.Fprintln(os.Stderr, "生成管理员凭证。注意：密码输入会回显，请确保周围无人。")
	fmt.Fprint(os.Stderr, "管理员密码: ")
	reader := bufio.NewReader(os.Stdin)
	pwd, err := reader.ReadString('\n')
	if err != nil && pwd == "" {
		fmt.Fprintln(os.Stderr, "读取密码失败:", err)
		os.Exit(1)
	}
	pwd = strings.TrimRight(pwd, "\r\n")
	if pwd == "" {
		fmt.Fprintln(os.Stderr, "密码不能为空")
		os.Exit(1)
	}
	if len(pwd) < 20 {
		fmt.Fprintln(os.Stderr, "警告：密码少于 20 字符，建议使用 20 位以上随机串。")
	}

	salt, hash, secret, err := config.GenerateCredentials(pwd, 600000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "\n把下面这段粘贴进 internal/config/config.go：")
	fmt.Fprintln(os.Stderr)
	fmt.Println(config.CredentialBlock(salt, hash, secret, 600000))
}
