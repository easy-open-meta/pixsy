// Command pixsy 是自托管轻量图库的单文件服务入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/pixsy/pixsy/internal/api"
	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/config"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/server"
	"github.com/pixsy/pixsy/internal/setting"
	"github.com/pixsy/pixsy/internal/storage"
	"github.com/pixsy/pixsy/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger) // 让 internal 各包的 slog 输出同走 JSON stdout
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	cfg, err := config.Load(args, os.Stderr)
	if err != nil {
		return err // flag 解析失败时已打印用法；--help 也走这里
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	db, err := model.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()

	settings, err := setting.Load(ctx, db, cfg.MaxUploadMB)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if cfg.ConfigFile != "" {
		logger.Info("config file loaded", "file", cfg.ConfigFile)
	}
	// 配置文件里出现的设置项每次启动都强制生效，覆盖后台页面的修改。
	if cfg.Settings.HasValues() {
		if _, err := settings.Update(ctx, setting.PatchFromFile(cfg.Settings)); err != nil {
			return fmt.Errorf("应用配置文件 %s 的 settings: %w", cfg.ConfigFile, err)
		}
		logger.Info("settings applied from config file", "file", cfg.ConfigFile)
	}

	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}

	secret, err := auth.LoadSecret(cfg.DataDir, cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("load jwt secret: %w", err)
	}
	mgr, err := auth.NewManager(secret)
	if err != nil {
		return fmt.Errorf("init auth: %w", err)
	}

	apiSrv := api.New(api.Options{
		DB:       db,
		Settings: settings,
		Store:    store,
		Auth:     mgr,
		BaseURL:  cfg.BaseURL,
	})

	srv := server.New(server.Options{
		API:       apiSrv.Handler(),
		Store:     store,
		DB:        db,
		Static:    web.Dist(),
		StaticDir: cfg.StaticDir,
		Logger:    logger,
	})

	// 先同步监听，确认端口可用再启动服务，也保证开浏览器时页面已能访问。
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr, "data", cfg.DataDir)
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	if cfg.OpenBrowser {
		if u := browseURL(cfg.Addr); u != "" {
			opened, err := openBrowser(u)
			switch {
			case err != nil:
				logger.Warn("open browser failed", "url", u, "err", err)
			case opened:
				logger.Info("browser opened", "url", u)
			}
		}
	}

	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	select {
	case err := <-errc:
		return err
	case <-stop.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

// browseURL 把监听地址转成本机浏览器可访问的页面地址。
func browseURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// openBrowser 用系统默认浏览器打开页面，报告是否真的打开了。
// 仅 Windows 生效：Linux/Docker 等服务器部署不应弹出浏览器。
func openBrowser(url string) (bool, error) {
	if runtime.GOOS != "windows" {
		return false, nil
	}
	if err := exec.Command("cmd", "/c", "start", "", url).Start(); err != nil {
		return false, err
	}
	return true, nil
}
