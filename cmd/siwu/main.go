// 司寤 siwu —— 网页变化监控与 RSS 订阅中枢。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Felix2yu/siwu/internal/events"
	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/httpapi"
	"github.com/Felix2yu/siwu/internal/notify"
	"github.com/Felix2yu/siwu/internal/pipeline"
	"github.com/Felix2yu/siwu/internal/probe"
	"github.com/Felix2yu/siwu/internal/scheduler"
	"github.com/Felix2yu/siwu/internal/secretbox"
	"github.com/Felix2yu/siwu/internal/store"
	"github.com/Felix2yu/siwu/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr     = flag.String("addr", env("SIWU_ADDR", ":8788"), "监听地址")
		dataDir  = flag.String("data", env("SIWU_DATA_DIR", "./data"), "数据目录")
		baseURL  = flag.String("base-url", env("SIWU_BASE_URL", ""), "对外地址，用于生成订阅链接")
		browser  = flag.String("browser-ws", env("SIWU_BROWSER_WS", ""), "外置浏览器 CDP 地址，如 ws://browser:9222")
		chrome   = flag.String("chrome-path", env("SIWU_CHROME_PATH", ""), "本机 Chrome/Chromium 路径（不配则自动查找）")
		workers  = flag.Int("workers", envInt("SIWU_WORKERS", 4), "并发抓取数")
		showVer  = flag.Bool("version", false, "打印版本号后退出")
		insecure = flag.Bool("allow-private", envBool("SIWU_ALLOW_PRIVATE", false), "允许访问内网地址")
	)
	// 允许 `siwu serve --addr ...` 与 `siwu --addr ...` 两种写法：
	// 标准库 flag 遇到第一个位置参数就停止解析，所以先把子命令剥掉。
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		return err
	}

	if *showVer {
		fmt.Println("siwu", version)
		return nil
	}

	level := slog.LevelInfo
	if envBool("SIWU_DEBUG", false) {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	st, err := store.Open(filepath.Join(*dataDir, "siwu.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.InterruptRunning(time.Now().Unix()); err != nil {
		log.Warn("清理残留运行记录失败", "err", err)
	}
	box, err := secretbox.LoadOrCreate(filepath.Join(*dataDir, "secret.key"))
	if err != nil {
		return err
	}

	fetcher := fetch.New(fetch.Options{
		AllowPrivate: *insecure,
		BrowserWS:    *browser,
		ChromePath:   *chrome,
		MaxBytes:     8 << 20,
	})
	notifier := notify.New(st, box, log)
	bus := events.New()
	notifier.Bus = bus
	deps := &pipeline.Deps{
		Store: st, Fetcher: fetcher, Notifier: notifier, Log: log,
		Location: time.Local,
	}
	sched := scheduler.New(deps, st, log, *workers)
	sched.Bus = bus
	pr := probe.New(fetcher)

	if *baseURL == "" {
		host := "127.0.0.1"
		port := *addr
		if i := lastIndexByte(port, ':'); i >= 0 {
			port = port[i:]
			if port == ":" {
				port = ":8788"
			}
		}
		*baseURL = "http://" + host + port
	}

	srv := &httpapi.Server{
		Store: st, Fetcher: fetcher, Probe: pr, Notifier: notifier,
		Scheduler: sched, Deps: deps, Box: box, Log: log, Bus: bus,
		BaseURL: *baseURL, Password: os.Getenv("SIWU_PASSWORD"),
		FeedToken: os.Getenv("SIWU_FEED_TOKEN"), Version: version,
		Workers: *workers, DataDir: *dataDir, AllowPrivate: *insecure,
		BrowserWS: *browser, ChromePath: *chrome,
	}
	if web.HasBuild() {
		srv.Web = web.FS()
	} else {
		log.Warn("前端产物缺失，只提供 API（先跑 ./scripts/build.sh）")
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go sched.Start(ctx)
	go notifier.Start(ctx)
	go func() {
		log.Info("司寤已启动", "version", version, "addr", *addr, "base_url", *baseURL,
			"browser", *browser != "", "auth", os.Getenv("SIWU_PASSWORD") != "",
			"feed_token", os.Getenv("SIWU_FEED_TOKEN") != "")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("收到退出信号，正在收尾")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
