// Package pipeline 是一次运行的唯一编排路径：试跑与定时跑只差 dryRun。
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/article"
	"github.com/Felix2yu/siwu/internal/extract"
	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/notify"
	"github.com/Felix2yu/siwu/internal/store"
	"github.com/Felix2yu/siwu/internal/transform"
	"github.com/microcosm-cc/bluemonday"
)

// Deps 是流水线依赖。
type Deps struct {
	Store    *store.Store
	Fetcher  *fetch.Fetcher
	Notifier *notify.Notifier
	Log      *slog.Logger
	Location *time.Location
}

// Outcome 是一次运行的产出。
type Outcome struct {
	Run            model.Run
	NewEntries     []model.Entry
	ChangedEntries []model.Entry
	ItemsFound     int
	Updated        int
	Changed        bool
	Warnings       []string
}

var sanitizer = bluemonday.UGCPolicy()

// Execute 跑一次。
func (d *Deps) Execute(ctx context.Context, src model.Source, trigger string, dryRun bool) (Outcome, error) {
	start := time.Now()
	out := Outcome{Run: model.Run{
		SourceID:  src.ID,
		StartedAt: start.Unix(),
		Trigger:   trigger,
		Status:    "ok",
	}}
	if !dryRun {
		id, err := d.Store.StartRun(src.ID, trigger, start.Unix())
		if err != nil {
			return out, fmt.Errorf("写运行记录: %w", err)
		}
		out.Run.ID = id
	}

	// 本次运行之前的历史状态：用于识别 recovery 与连续空跑
	var (
		prevStatus  string
		emptyStreak int
	)
	if !dryRun {
		if statuses, err := d.Store.RecentStatuses(src.ID, 10); err == nil {
			if len(statuses) > 0 {
				prevStatus = statuses[0]
			}
			for _, st := range statuses {
				if st != "empty" {
					break
				}
				emptyStreak++
			}
		}
	}

	var (
		runErr error
	)
	switch src.Kind {
	case model.KindPage:
		runErr = d.runPage(ctx, src, dryRun, &out)
	case model.KindJSON:
		runErr = d.runJSON(ctx, src, dryRun, &out)
	case model.KindRSS:
		runErr = d.runFeed(ctx, src, dryRun, &out)
	default:
		runErr = d.runList(ctx, src, dryRun, &out)
	}

	out.Run.FinishedAt = time.Now().Unix()
	out.Run.DurationMS = time.Since(start).Milliseconds()
	out.Run.ItemsFound = out.ItemsFound
	out.Run.ItemsNew = len(out.NewEntries)
	out.Run.ItemsUpdated = out.Updated
	if runErr != nil {
		out.Run.Status = "error"
		out.Run.Error = runErr.Error()
	} else if out.ItemsFound == 0 {
		out.Run.Status = "empty"
		out.Warnings = append(out.Warnings, "本次没有抽取到条目，选择器可能已失效")
	}
	out.Run.Warnings = out.Warnings
	if !dryRun {
		if err := d.Store.FinishRun(out.Run); err != nil {
			d.Log.Error("收尾运行记录失败", "err", err)
		}
		if d.Notifier != nil {
			// 通知失败只作为 warning 暴露，不影响入库与 RSS
			warns := d.Notifier.Dispatch(ctx, notify.Event{
				Source:      src,
				Entries:     out.NewEntries,
				Changed:     out.ChangedEntries,
				Status:      out.Run.Status,
				PrevStatus:  prevStatus,
				Err:         out.Run.Error,
				SilentCount: emptyStreak + boolInt(out.Run.Status == "empty"),
			})
			if len(warns) > 0 {
				out.Warnings = append(out.Warnings, warns...)
				if err := d.Store.AppendRunWarnings(out.Run.ID, warns); err != nil {
					d.Log.Warn("回写通知告警失败", "err", err)
				}
			}
		}
	}
	return out, runErr
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (d *Deps) runList(ctx context.Context, src model.Source, dryRun bool, out *Outcome) error {
	resp, err := d.Fetcher.Page(ctx, src) // 按 src.Engine 走 http 或 browser
	if err != nil {
		return err
	}
	base := firstNonEmpty(resp.FinalURL, src.URL)
	res, err := extract.HTML(resp.Body, base, src.Extract, src.Rules)
	if err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, res.Warnings...)
	out.ItemsFound = len(res.Items)
	return d.persist(ctx, src, base, res.Items, dryRun, out)
}

func (d *Deps) runJSON(ctx context.Context, src model.Source, dryRun bool, out *Outcome) error {
	resp, err := d.Fetcher.API(ctx, src)
	if err != nil {
		return err
	}
	res, err := extract.JSON(resp.Body, src.Extract, src.Rules)
	if err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, res.Warnings...)
	out.ItemsFound = len(res.Items)
	return d.persist(ctx, src, firstNonEmpty(src.URL, resp.FinalURL), res.Items, dryRun, out)
}

func (d *Deps) runFeed(ctx context.Context, src model.Source, dryRun bool, out *Outcome) error {
	resp, err := d.Fetcher.Page(ctx, src)
	if err != nil {
		return err
	}
	items, title, warnings, err := parseFeed(resp.Body)
	if err != nil {
		return err
	}
	if src.Name == "" && title != "" {
		src.Name = title
	}
	out.Warnings = append(out.Warnings, warnings...)
	out.ItemsFound = len(items)
	return d.persist(ctx, src, src.URL, items, dryRun, out)
}

// persist 是 html/json/rss 三种源的共同收尾：过滤、去重、补正文、入库。
func (d *Deps) persist(ctx context.Context, src model.Source, base string, items []extract.RawItem, dryRun bool, out *Outcome) error {
	now := time.Now().Unix()
	for _, it := range items {
		if !keep(src.Filter, it) {
			continue
		}
		guid := firstNonEmpty(it.GUID, it.Link, it.Title)
		if guid == "" {
			continue
		}
		key := hashKey(src.ID, guid)
		published := transform.ParseDate(it.Date)
		entry := model.Entry{
			SourceID:    src.ID,
			EntryKey:    key,
			GUID:        guid,
			Title:       it.Title,
			Link:        it.Link,
			Summary:     it.Summary,
			ContentHTML: sanitize(it.Content),
			ContentText: transform.RemoveTags(it.Content),
			Category:    it.Category,
			PublishedAt: published,
			FirstSeenAt: now,
			UpdatedAt:   now,
		}
		if entry.PublishedAt == 0 {
			entry.PublishedAt = now
		}
		if entry.Summary == "" && entry.ContentText != "" {
			entry.Summary = truncate(entry.ContentText, 180)
		}

		exists := false
		if !dryRun {
			var err error
			exists, err = d.Store.HasEntry(src.ID, key)
			if err != nil {
				return err
			}
		}
		// 只对新条目抓正文，稳态零额外请求
		if !exists && src.Article.Enabled && entry.Link != "" {
			if art, err := d.fetchArticle(ctx, src, base, entry.Link); err == nil {
				if art.Title != "" && entry.Title == "" {
					entry.Title = art.Title
				}
				if art.ContentHTML != "" {
					entry.ContentHTML = art.ContentHTML
					entry.ContentText = art.ContentText
					if entry.Summary == "" {
						entry.Summary = truncate(art.ContentText, 180)
					}
				}
				if entry.PublishedAt == 0 {
					if ts := transform.ParseDate(art.Date); ts > 0 {
						entry.PublishedAt = ts
					}
				}
			} else {
				out.Warnings = append(out.Warnings, fmt.Sprintf("取正文失败 %s: %v", entry.Link, err))
			}
		}
		// 判重前先按 Normalize 正则剔除噪声（访问量、时间戳等）。
		// 三个字段都要过一遍：站点常把计数塞在标题或摘要里，
		// 只归一化正文会让「只有计数变了」被当成内容更新。
		hashTitle, hashSummary, hashText := entry.Title, entry.Summary, entry.ContentText
		for _, pat := range src.Extract.Normalize {
			re, err := compile(pat)
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("归一化正则非法 %q", pat))
				continue
			}
			hashTitle = re.ReplaceAllString(hashTitle, "")
			hashSummary = re.ReplaceAllString(hashSummary, "")
			hashText = re.ReplaceAllString(hashText, "")
		}
		entry.ContentHash = hashContent(hashTitle, hashSummary, hashText)

		if dryRun {
			entry.ID = int64(len(out.NewEntries) + 1)
			out.NewEntries = append(out.NewEntries, entry)
			continue
		}
		inserted, updated, err := d.Store.UpsertEntry(&entry, src.Policy.UpdateMode)
		if err != nil {
			return err
		}
		if inserted {
			out.NewEntries = append(out.NewEntries, entry)
		}
		if updated {
			out.Updated++
			out.Changed = true
			out.ChangedEntries = append(out.ChangedEntries, entry)
		}
	}

	if !dryRun {
		d.pruneOld(src)
	}
	return nil
}

// pruneOld 按保留天数清理旧条目。max_items 只控制订阅输出条数，不删库。
func (d *Deps) pruneOld(src model.Source) {
	if src.Policy.RetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -src.Policy.RetentionDays).Unix()
	if err := d.Store.Prune(src.ID, cutoff); err != nil {
		d.Log.Warn("清理旧条目失败", "source", src.Slug, "err", err)
	}
}

func (d *Deps) fetchArticle(ctx context.Context, src model.Source, base, link string) (article.Result, error) {
	detail := src
	detail.URL = link
	detail.Request.API = nil
	resp, err := d.Fetcher.Page(ctx, detail)
	if err != nil {
		return article.Result{}, err
	}
	return article.Extract(resp.Body, firstNonEmpty(resp.FinalURL, link), src.Article)
}

func (d *Deps) runPage(ctx context.Context, src model.Source, dryRun bool, out *Outcome) error {
	resp, err := d.Fetcher.Page(ctx, src)
	if err != nil {
		return err
	}
	text, regionHTML, warnings, err := extract.Snapshot(resp.Body, src.URL, src.Extract)
	if err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, warnings...)
	out.ItemsFound = 1

	sum := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(sum[:])
	prev, err := d.Store.GetSnapshot(src.ID)
	if err != nil {
		return err
	}
	if prev.Hash == hash {
		return nil
	}
	// 变化行数阈值：页面上的访问量、相对时间这类噪声常常让哈希每次都变，
	// 低于阈值就当没变化，不为一条噪声写一条订阅。
	added, removed := diffStat(prev.Text, text)
	if prev.Text != "" && src.Policy.MinChangedLines > 0 && added+removed < src.Policy.MinChangedLines {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"内容变化 %d 行，未达阈值 %d 行，本次不产出条目", added+removed, src.Policy.MinChangedLines))
		return nil
	}
	out.Changed = true

	now := time.Now()
	title := fmt.Sprintf("%s 内容变化 · %s", src.Name, now.Format("2006-01-02 15:04"))
	body := diffHTML(prev.Text, text)
	if prev.Text == "" {
		title = fmt.Sprintf("%s 首次快照 · %s", src.Name, now.Format("2006-01-02 15:04"))
		body = "<pre>" + html.EscapeString(text) + "</pre>"
	}
	entry := model.Entry{
		SourceID:    src.ID,
		EntryKey:    "snap:" + hash,
		GUID:        "siwu:" + src.Slug + ":" + hash[:16],
		Title:       title,
		Link:        src.URL,
		Summary:     truncate(text, 180),
		ContentHTML: body,
		ContentText: text,
		PublishedAt: now.Unix(),
		FirstSeenAt: now.Unix(),
		UpdatedAt:   now.Unix(),
		ContentHash: hash,
	}
	if regionHTML != "" {
		entry.ContentHTML = body + "\n<hr>\n" + sanitize(regionHTML)
	}
	if !dryRun {
		if _, _, err := d.Store.UpsertEntry(&entry, "archive"); err != nil {
			return err
		}
		if err := d.Store.PutSnapshot(src.ID, store.Snapshot{Hash: hash, Text: text, CapturedAt: now.Unix()}); err != nil {
			return err
		}
		d.pruneOld(src)
	}
	out.NewEntries = append(out.NewEntries, entry)
	return nil
}

// ---------------------------------------------------------------- helpers

func keep(f model.Filter, it extract.RawItem) bool {
	value := it.Title
	switch f.Field {
	case "summary":
		value = it.Summary
	case "link":
		value = it.Link
	case "category":
		value = it.Category
	}
	match := func(pattern string) bool {
		if f.Regex {
			re, err := compile(pattern)
			if err != nil {
				return false
			}
			return re.MatchString(value)
		}
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
	}
	if len(f.Include) > 0 {
		hit := false
		for _, p := range f.Include {
			if match(p) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, p := range f.Exclude {
		if match(p) {
			return false
		}
	}
	return true
}

func hashKey(sourceID int64, guid string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s", sourceID, guid)))
	return hex.EncodeToString(sum[:])
}

func hashContent(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:12])
}

func sanitize(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	return sanitizer.Sanitize(fragment)
}

func truncate(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// diffStat 统计新增与消失的非空行数，供变化阈值判断。
func diffStat(oldText, newText string) (added, removed int) {
	oldSet := map[string]bool{}
	for _, l := range strings.Split(oldText, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			oldSet[t] = true
		}
	}
	newSet := map[string]bool{}
	for _, l := range strings.Split(newText, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		newSet[t] = true
		if !oldSet[t] {
			added++
		}
	}
	for l := range oldSet {
		if !newSet[l] {
			removed++
		}
	}
	return added, removed
}

// diffHTML 生成行级变化摘要：只标出新增与消失的行，够读即可。
func diffHTML(oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	oldSet := map[string]bool{}
	for _, l := range oldLines {
		oldSet[strings.TrimSpace(l)] = true
	}
	newSet := map[string]bool{}
	for _, l := range newLines {
		newSet[strings.TrimSpace(l)] = true
	}
	var b strings.Builder
	b.WriteString("<pre class=\"siwu-diff\">")
	for _, l := range newLines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !oldSet[t] {
			b.WriteString("+ " + html.EscapeString(t) + "\n")
		} else {
			b.WriteString("  " + html.EscapeString(t) + "\n")
		}
	}
	for _, l := range oldLines {
		t := strings.TrimSpace(l)
		if t == "" || newSet[t] {
			continue
		}
		b.WriteString("- " + html.EscapeString(t) + "\n")
	}
	b.WriteString("</pre>")
	return b.String()
}

// NormalizeBase 把 base 规整成可用于相对链接解析的绝对地址。
func NormalizeBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return raw
	}
	u.Fragment = ""
	return u.String()
}
