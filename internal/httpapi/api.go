package httpapi

import (
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/cronexpr"
	"github.com/Felix2yu/siwu/internal/extract"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/notify"
	"github.com/Felix2yu/siwu/internal/scheduler"
	"github.com/Felix2yu/siwu/internal/transform"
	"github.com/unraid/apprise-go"
)

// ---------------------------------------------------------------- sources

type sourceView struct {
	model.Source
	EntryCount int    `json:"entry_count"`
	FeedRSS    string `json:"feed_rss"`
	FeedAtom   string `json:"feed_atom"`
	FeedJSON   string `json:"feed_json"`
}

func (s *Server) view(src model.Source) sourceView {
	n, _ := s.Store.CountEntries(src.ID)
	base := strings.TrimRight(s.BaseURL, "/")
	suffix := ""
	if s.FeedToken != "" {
		suffix = "?k=" + s.FeedToken
	}
	return sourceView{
		Source:     src,
		EntryCount: n,
		FeedRSS:    fmt.Sprintf("%s/f/%s.xml%s", base, src.Slug, suffix),
		FeedAtom:   fmt.Sprintf("%s/f/%s.atom%s", base, src.Slug, suffix),
		FeedJSON:   fmt.Sprintf("%s/f/%s.json%s", base, src.Slug, suffix),
	}
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSources()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]sourceView, 0, len(list))
	for _, src := range list {
		out = append(out, s.view(src))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	src, err := s.Store.GetSource(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "源不存在"})
		return
	}
	writeJSON(w, http.StatusOK, s.view(src))
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var src model.Source
	if err := decodeJSON(r, &src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.normalizeAndValidate(&src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.Store.SaveSource(&src); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("保存失败（slug 可能重复）: %v", err)})
		return
	}
	s.Scheduler.Kick()
	writeJSON(w, http.StatusCreated, s.view(src))
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	old, err := s.Store.GetSource(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "源不存在"})
		return
	}
	var src model.Source
	if err := decodeJSON(r, &src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	src.ID = id
	src.CreatedAt = old.CreatedAt
	src.NextRunAt = old.NextRunAt
	if src.Slug == "" {
		src.Slug = old.Slug
	}
	if err := s.normalizeAndValidate(&src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.Store.SaveSource(&src); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.view(src))
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	if err := s.Store.DeleteSource(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) runSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	src, err := s.Store.GetSource(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "源不存在"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, runErr := s.Scheduler.RunSource(ctx, src, "manual")
	if errors.Is(runErr, scheduler.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该源正在运行，请稍候"})
		return
	}
	resp := map[string]any{
		"run":      out.Run,
		"items":    out.NewEntries,
		"updated":  out.Updated,
		"warnings": out.Warnings,
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// tryDraft 用草案配置试跑一次，不写库（用于编辑器里的「试跑」与 json 型预览）。
func (s *Server) tryDraft(w http.ResponseWriter, r *http.Request) {
	var src model.Source
	if err := decodeJSON(r, &src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	src.ID = 0
	if err := s.normalizeAndValidate(&src); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, err := s.Deps.Execute(ctx, src, "test", true)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "warnings": out.Warnings})
		return
	}
	// 试跑结果给编辑器的表格用：字段形状与 probe/extract 的 RawItem 保持一致，
	// 否则前端的日期列会因为 Entry.published_at 与 RawItem.date 不同名而空掉。
	items := make([]map[string]any, 0, len(out.NewEntries))
	for _, e := range out.NewEntries {
		date := ""
		if e.PublishedAt > 0 {
			date = time.Unix(e.PublishedAt, 0).Format("2006-01-02 15:04")
		}
		items = append(items, map[string]any{
			"title": e.Title, "link": e.Link, "date": date,
			"summary": e.Summary, "category": e.Category,
			"content": e.ContentHTML, "guid": e.GUID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"found":    out.ItemsFound,
		"warnings": out.Warnings,
	})
}

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	entries, err := s.Store.Entries(id, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	total, _ := s.Store.CountEntries(id)
	writeJSON(w, http.StatusOK, map[string]any{"items": entries, "total": total})
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	if err := s.Store.DeleteEntry(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	var sourceID int64
	if v := r.URL.Query().Get("source_id"); v != "" {
		sourceID, _ = strconv.ParseInt(v, 10, 64)
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.Store.ListRuns(sourceID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// ---------------------------------------------------------------- probe

func (s *Server) probeFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL     string            `json:"url"`
		Engine  string            `json:"engine"`
		Headers map[string]string `json:"headers"`
		Cookies map[string]string `json:"cookies"`
		Proxy   string            `json:"proxy"`
		WaitMS  int               `json:"wait_ms"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url 必填"})
		return
	}
	engine := model.Engine(body.Engine)
	if engine == "" {
		engine = model.EngineHTTP
	}
	req := model.Request{Headers: body.Headers, Cookies: body.Cookies, Proxy: body.Proxy, WaitMS: body.WaitMS, TimeoutSec: 45}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	sess, err := s.Probe.Fetch(ctx, body.URL, engine, req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sid":   sess.ID,
		"url":   sess.URL,
		"title": sess.Title,
		"bytes": len(sess.HTML),
	})
}

func (s *Server) previewPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.Probe.Get(r.PathValue("sid"))
	if !ok {
		http.Error(w, "预览已过期，请重新抓取", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// 预览页要能在 iframe 里被加载：明确不设置 X-Frame-Options，也不加 CSP。
	_, _ = w.Write(sess.HTML)
}

func (s *Server) probeExtract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SID     string        `json:"sid"`
		Extract model.Extract `json:"extract"`
		Rules   []model.Rule  `json:"rules"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.Probe.Extract(body.SID, body.Extract, body.Rules)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": res.Items, "matched": res.Matched, "warnings": res.Warnings,
	})
}

func (s *Server) probeCount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SID      string `json:"sid"`
		Selector string `json:"selector"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	n, samples, err := s.Probe.Count(body.SID, body.Selector)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "samples": samples})
}

func (s *Server) probeSuggest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SID   string `json:"sid"`
		XPath string `json:"xpath"`
		Item  string `json:"item"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sug, err := s.Probe.Suggest(body.SID, body.XPath, body.Item)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

// ---------------------------------------------------------------- targets

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTargets(s.Box)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var t model.Target
	if err := decodeJSON(r, &t); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	t.Enabled = true
	if err := notify.Validate(t.URL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Apprise 地址不可用: " + err.Error()})
		return
	}
	t.Kind = schemeOf(t.URL)
	if err := s.Store.SaveTarget(&t, s.Box); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	t.URL = ""
	t.Masked = "已保存"
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) updateTarget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	var t model.Target
	if err := decodeJSON(r, &t); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	t.ID = id
	if t.URL != "" {
		if err := notify.Validate(t.URL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Apprise 地址不可用: " + err.Error()})
			return
		}
		t.Kind = schemeOf(t.URL)
	} else {
		// 未改地址：沿用旧值（含协议类型），否则改名会把 kind 清空
		list, _ := s.Store.ListTargets(s.Box)
		for _, old := range list {
			if old.ID == id {
				t.URL = old.URL
				if t.Kind == "" {
					t.Kind = old.Kind
				}
			}
		}
	}
	if err := s.Store.SaveTarget(&t, s.Box); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 非法"})
		return
	}
	if err := s.Store.DeleteTarget(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) testTargets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string  `json:"url"`
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	urls := []string{}
	if body.URL != "" {
		urls = append(urls, body.URL)
	}
	if len(body.IDs) > 0 {
		got, err := s.Store.TargetURLs(body.IDs, s.Box)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		urls = append(urls, got...)
	}
	if len(urls) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有可发送的目标"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Notifier.Send(ctx, urls, "司寤 siwu 测试通知", "<p>如果你收到这条消息，说明通知通道已打通。</p>", apprise.NotifyInfo); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": len(urls)})
}

func (s *Server) schemas(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schemas": notify.Schemas()})
}

// ---------------------------------------------------------------- overview / events

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetStats(time.Now().Unix())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// sse 推送运行与通知事件。前端用 EventSource 订阅，断线后浏览器会自动重连。
func (s *Server) sse(w http.ResponseWriter, r *http.Request) {
	if s.Bus == nil {
		http.Error(w, "事件总线未启用", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "当前连接不支持流式输出", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	ch, cancel := s.Bus.Subscribe()
	defer cancel()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// exportOPML 导出全部订阅，可直接导入 Feedly / NetNewsWire / Inoreader。
func (s *Server) exportOPML(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSources()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type outline struct {
		Text    string `xml:"text,attr"`
		Title   string `xml:"title,attr"`
		Type    string `xml:"type,attr"`
		XMLURL  string `xml:"xmlUrl,attr"`
		HTMLURL string `xml:"htmlUrl,attr,omitempty"`
	}
	type body struct {
		Outlines []outline `xml:"outline"`
	}
	type head struct {
		Title string `xml:"title"`
	}
	doc := struct {
		XMLName xml.Name `xml:"opml"`
		Version string   `xml:"version,attr"`
		Head    head     `xml:"head"`
		Body    body     `xml:"body"`
	}{
		Version: "2.0",
		Head:    head{Title: "司寤 siwu 订阅"},
	}
	suffix := ""
	if s.FeedToken != "" {
		suffix = "?k=" + s.FeedToken
	}
	for _, src := range list {
		if !src.Enabled {
			continue
		}
		doc.Body.Outlines = append(doc.Body.Outlines, outline{
			Text:    src.Name,
			Title:   src.Name,
			Type:    "rss",
			XMLURL:  fmt.Sprintf("%s/f/%s.xml%s", strings.TrimRight(s.BaseURL, "/"), src.Slug, suffix),
			HTMLURL: src.URL,
		})
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="siwu.opml"`)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

// flushNotify 手动冲刷 batch/digest 队列（测试与关闭前用）。
func (s *Server) flushNotify(w http.ResponseWriter, r *http.Request) {
	if s.Notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "通知未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	s.Notifier.Flush(ctx)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) transforms(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ops": transform.Ops})
}

// ---------------------------------------------------------------- validation

func (s *Server) normalizeAndValidate(src *model.Source) error {
	src.Name = strings.TrimSpace(src.Name)
	src.URL = strings.TrimSpace(src.URL)
	if src.Name == "" {
		return fmt.Errorf("名称必填")
	}
	if src.URL == "" {
		return fmt.Errorf("URL 必填")
	}
	src.Defaults()
	if src.Slug == "" {
		src.Slug = model.Slug(src.Name, src.URL)
	}
	if err := transform.Validate(src.Rules); err != nil {
		return err
	}
	switch src.Kind {
	case model.KindHTML:
		if strings.TrimSpace(src.Extract.Item) == "" {
			return fmt.Errorf("列表型必须给出条目选择器")
		}
		if err := validateSelector(src.Extract.Item); err != nil {
			return err
		}
		if _, ok := src.Extract.Fields["title"]; !ok {
			if _, ok := src.Extract.Fields["link"]; !ok {
				return fmt.Errorf("至少需要一个 title 或 link 字段")
			}
		}
	case model.KindJSON:
		if src.Request.API == nil || strings.TrimSpace(src.Request.API.URL) == "" {
			return fmt.Errorf("接口型必须给出 api.url")
		}
		if !strings.HasPrefix(strings.TrimSpace(src.Extract.Item), "$") {
			return fmt.Errorf("接口型的 item 必须是 JSON 路径（以 $ 开头）")
		}
		if strings.TrimSpace(src.Request.API.URL) == "" {
			return fmt.Errorf("api.url 不能为空")
		}
	case model.KindPage:
		if strings.TrimSpace(src.Extract.Scope) != "" {
			if err := validateSelector(src.Extract.Scope); err != nil {
				return err
			}
		}
	}
	for name, f := range src.Extract.Fields {
		if strings.TrimSpace(f.Sel) == "" {
			continue
		}
		if src.Kind == model.KindJSON {
			if !strings.HasPrefix(f.Sel, "$") && f.Template == "" {
				return fmt.Errorf("字段 %s 的 JSON 路径必须以 $ 开头", name)
			}
			continue
		}
		if err := validateSelector(f.Sel); err != nil {
			return fmt.Errorf("字段 %s: %w", name, err)
		}
		for _, fb := range f.Fallback {
			if err := validateSelector(fb); err != nil {
				return fmt.Errorf("字段 %s 的 fallback: %w", name, err)
			}
		}
	}
	if strings.TrimSpace(src.Article.Content) != "" {
		if err := validateSelector(src.Article.Content); err != nil {
			return fmt.Errorf("正文选择器: %w", err)
		}
	}
	// 调度与通知策略的枚举与格式校验：宁可保存时报错，也不要运行期静默失效
	if cron := strings.TrimSpace(src.Policy.Cron); cron != "" {
		if _, err := cronexpr.Parse(cron); err != nil {
			return fmt.Errorf("cron 表达式: %w", err)
		}
	}
	switch src.Policy.UpdateMode {
	case "ignore", "update", "archive":
	default:
		return fmt.Errorf("去重策略 %q 不受支持（ignore / update / archive）", src.Policy.UpdateMode)
	}
	switch src.Policy.Notify.Timing {
	case "immediate", "batch", "digest":
	default:
		return fmt.Errorf("通知时机 %q 不受支持（immediate / batch / digest）", src.Policy.Notify.Timing)
	}
	switch src.Policy.Notify.Detail {
	case "none", "summary", "full":
	default:
		return fmt.Errorf("通知详略 %q 不受支持（none / summary / full）", src.Policy.Notify.Detail)
	}
	if q := strings.TrimSpace(src.Policy.Notify.QuietHours); q != "" {
		if !validWindow(q) {
			return fmt.Errorf("静默时段格式应为 HH:MM-HH:MM，收到 %q", q)
		}
	}
	if d := strings.TrimSpace(src.Policy.Notify.DigestAt); d != "" {
		if !validClock(d) {
			return fmt.Errorf("汇总时刻格式应为 HH:MM，收到 %q", d)
		}
	}
	for name, f := range src.Extract.Fields {
		for _, tr := range f.Transforms {
			found := false
			for _, r := range src.Rules {
				if r.ID == tr {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("字段 %s 引用了不存在的清洗规则 %q", name, tr)
			}
		}
	}
	for _, pat := range src.Extract.Normalize {
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("归一化正则 %q 非法: %w", pat, err)
		}
	}
	for _, pat := range src.Extract.IgnoreRegex {
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("忽略正则 %q 非法: %w", pat, err)
		}
	}
	if src.Filter.Regex {
		for _, pat := range append(append([]string{}, src.Filter.Include...), src.Filter.Exclude...) {
			if _, err := regexp.Compile(pat); err != nil {
				return fmt.Errorf("过滤正则 %q 非法: %w", pat, err)
			}
		}
	}
	for _, ex := range src.Extract.Exclude {
		if err := validateSelector(ex); err != nil {
			return fmt.Errorf("排除选择器: %w", err)
		}
	}
	if src.Policy.MinChangedLines < 0 {
		return fmt.Errorf("变化行数阈值不能为负数")
	}
	return nil
}

// schemeOf 取 Apprise 地址的协议名，作为目标类型展示。
func schemeOf(raw string) string {
	if i := strings.Index(raw, "://"); i > 0 {
		return strings.ToLower(raw[:i])
	}
	return ""
}

func validClock(s string) bool {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	return e1 == nil && e2 == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59
}

func validWindow(s string) bool {
	parts := strings.SplitN(strings.TrimSpace(s), "-", 2)
	return len(parts) == 2 && validClock(parts[0]) && validClock(parts[1])
}

func validateSelector(raw string) error {
	_, err := extract.Parse(raw)
	return err
}
