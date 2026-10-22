// Package httpapi 提供 REST、SSE、预览与订阅端点。
package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/events"
	"github.com/Felix2yu/siwu/internal/feedgen"
	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/notify"
	"github.com/Felix2yu/siwu/internal/pipeline"
	"github.com/Felix2yu/siwu/internal/probe"
	"github.com/Felix2yu/siwu/internal/scheduler"
	"github.com/Felix2yu/siwu/internal/secretbox"
	"github.com/Felix2yu/siwu/internal/store"
)

// Server 聚合所有依赖。
type Server struct {
	Store     *store.Store
	Fetcher   *fetch.Fetcher
	Probe     *probe.Probe
	Notifier  *notify.Notifier
	Scheduler *scheduler.Scheduler
	Deps      *pipeline.Deps
	Box       *secretbox.Box
	Log       *slog.Logger
	Bus       *events.Bus
	Web       fs.FS
	BaseURL   string
	Password  string
	FeedToken string
	Version   string

	sessionToken string
	csrf         *http.CrossOriginProtection
}

// Handler 组装全部路由。
func (s *Server) Handler() http.Handler {
	if s.Password != "" {
		sum := sha256.Sum256([]byte("siwu-session|" + s.Password))
		s.sessionToken = hex.EncodeToString(sum[:])
	}
	s.csrf = http.NewCrossOriginProtection()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.health)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)

	mux.HandleFunc("GET /api/sources", s.listSources)
	mux.HandleFunc("POST /api/sources", s.createSource)
	mux.HandleFunc("PUT /api/sources/{id}", s.updateSource)
	mux.HandleFunc("GET /api/sources/{id}", s.getSource)
	mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)
	mux.HandleFunc("POST /api/sources/{id}/run", s.runSource)
	mux.HandleFunc("GET /api/sources/{id}/entries", s.listEntries)
	mux.HandleFunc("POST /api/try", s.tryDraft)
	mux.HandleFunc("DELETE /api/entries/{id}", s.deleteEntry)
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/events", s.sse)
	mux.HandleFunc("GET /api/export/opml", s.exportOPML)
	mux.HandleFunc("POST /api/notify/flush", s.flushNotify)

	mux.HandleFunc("POST /api/probe/fetch", s.probeFetch)
	mux.HandleFunc("GET /api/preview/{sid}", s.previewPage)
	mux.HandleFunc("GET /picker.js", s.picker)
	mux.HandleFunc("POST /api/probe/extract", s.probeExtract)
	mux.HandleFunc("POST /api/probe/count", s.probeCount)
	mux.HandleFunc("POST /api/probe/suggest", s.probeSuggest)

	mux.HandleFunc("GET /api/targets", s.listTargets)
	mux.HandleFunc("POST /api/targets", s.createTarget)
	mux.HandleFunc("PUT /api/targets/{id}", s.updateTarget)
	mux.HandleFunc("DELETE /api/targets/{id}", s.deleteTarget)
	mux.HandleFunc("POST /api/targets/test", s.testTargets)
	mux.HandleFunc("GET /api/schemas", s.schemas)
	mux.HandleFunc("GET /api/transforms", s.transforms)

	mux.HandleFunc("GET /f/{name}", s.feed)

	mux.HandleFunc("GET /", s.static)

	return s.withAuth(s.csrf.Handler(mux))
}

// ---------------------------------------------------------------- middleware

func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.Password == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/healthz",
			r.URL.Path == "/api/login",
			strings.HasPrefix(r.URL.Path, "/f/"),
			strings.HasPrefix(r.URL.Path, "/picker.js"),
			r.URL.Path == "/api/me":
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("siwu_session")
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "需要登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.Version})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.Password != "" && subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.Password)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "口令不正确"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "siwu_session", Value: s.sessionToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "siwu_session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	authed := s.Password == ""
	if !authed {
		if c, err := r.Cookie("siwu_session"); err == nil &&
			subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionToken)) == 1 {
			authed = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": authed, "auth_required": s.Password != ""})
}

// ---------------------------------------------------------------- static

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if s.Web == nil {
		http.Error(w, "前端未构建：先执行 ./scripts/build.sh", http.StatusServiceUnavailable)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.Web, path); err != nil {
		// SPA 回退
		path = "index.html"
	}
	data, err := fs.ReadFile(s.Web, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
}

func (s *Server) picker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(probe.PickerJS())
}

// ---------------------------------------------------------------- feeds

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	slug, ext, ok := strings.Cut(name, ".")
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 令牌保护：阅读器不方便带 Cookie，所以私有订阅用 ?k= 作为独立凭证
	if s.FeedToken != "" {
		got := r.URL.Query().Get("k")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.FeedToken)) != 1 {
			http.Error(w, "订阅令牌缺失或不正确", http.StatusUnauthorized)
			return
		}
	}
	var format feedgen.Format
	switch strings.ToLower(ext) {
	case "xml", "rss":
		format = feedgen.FormatRSS
	case "atom":
		format = feedgen.FormatAtom
	case "json":
		format = feedgen.FormatJSONFeed
	default:
		http.NotFound(w, r)
		return
	}
	src, err := s.Store.GetSourceBySlug(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !src.Enabled {
		http.Error(w, "该订阅已停用", http.StatusGone)
		return
	}
	entries, err := s.Store.Entries(src.ID, src.Policy.MaxItems, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	feedURL := fmt.Sprintf("%s/f/%s.%s", strings.TrimRight(s.BaseURL, "/"), slug, ext)
	if s.FeedToken != "" {
		feedURL += "?k=" + s.FeedToken
	}
	body, err := feedgen.Build(src, entries, format, feedURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag := feedgen.ETag(body)
	lastMod := feedgen.LastModified(entries)
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", lastMod.UTC().Format(http.TimeFormat))
	w.Header().Set("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if since := r.Header.Get("If-Modified-Since"); since != "" && r.Header.Get("If-None-Match") == "" {
		if t, err := http.ParseTime(since); err == nil && !lastMod.After(t.Add(time.Second)) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	switch format {
	case feedgen.FormatRSS:
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	case feedgen.FormatAtom:
		w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/feed+json; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	// 订阅体经常上万字节，gzip 能省一半以上；短响应不值得压缩
	if len(body) > 512 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(body)
		_ = gz.Close()
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------- json helpers

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.UnmarshalRead(r.Body, dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}
