// Package store 是 SQLite 持久层：迁移、源、条目、快照、运行记录、通知目标。
package store

import (
	"database/sql"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/secretbox"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store 是所有持久化的入口。写操作串行化，读操作走连接池。
type Store struct {
	db   *sql.DB
	path string     // 主库文件路径，DataDir() 由它推导
	mu   sync.Mutex // 单写者
	now  func() time.Time
}

// Open 打开（必要时创建）数据库并执行迁移。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Store{db: db, path: path, now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	for _, name := range names {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, name, s.now().Unix()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- sources

const sourceCols = `id, slug, name, kind, url, engine, enabled,
	request_json, extract_json, rules_json, filter_json, article_json, policy_json,
	created_at, updated_at, last_run_at, next_run_at, consecutive_failures`

func scanSource(sc interface{ Scan(...any) error }) (model.Source, error) {
	var (
		src                                                     model.Source
		reqJSON, extJSON, rulesJSON, filtJSON, artJSON, polJSON string
	)
	err := sc.Scan(&src.ID, &src.Slug, &src.Name, &src.Kind, &src.URL, &src.Engine, &src.Enabled,
		&reqJSON, &extJSON, &rulesJSON, &filtJSON, &artJSON, &polJSON,
		&src.CreatedAt, &src.UpdatedAt, &src.LastRunAt, &src.NextRunAt, &src.Failures)
	if err != nil {
		return src, err
	}
	for _, item := range []struct {
		raw string
		dst any
	}{
		{reqJSON, &src.Request}, {extJSON, &src.Extract}, {rulesJSON, &src.Rules},
		{filtJSON, &src.Filter}, {artJSON, &src.Article}, {polJSON, &src.Policy},
	} {
		if strings.TrimSpace(item.raw) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(item.raw), item.dst); err != nil {
			return src, fmt.Errorf("decode source json: %w", err)
		}
	}
	src.Defaults()
	return src, nil
}

// ListSources 返回全部源，按 id 升序。
func (s *Store) ListSources() ([]model.Source, error) {
	rows, err := s.db.Query(`SELECT ` + sourceCols + ` FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// GetSource 按 id 取源。
func (s *Store) GetSource(id int64) (model.Source, error) {
	row := s.db.QueryRow(`SELECT `+sourceCols+` FROM sources WHERE id = ?`, id)
	return scanSource(row)
}

// GetSourceBySlug 按 slug 取源。
func (s *Store) GetSourceBySlug(slug string) (model.Source, error) {
	row := s.db.QueryRow(`SELECT `+sourceCols+` FROM sources WHERE slug = ?`, slug)
	return scanSource(row)
}

// SaveSource 插入或更新（id==0 时插入）。
func (s *Store) SaveSource(src *model.Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src.Defaults()
	now := s.now().Unix()
	if src.Slug == "" {
		src.Slug = model.Slug(src.Name, src.URL)
	}
	req, err := json.Marshal(src.Request)
	if err != nil {
		return err
	}
	ext, err := json.Marshal(src.Extract)
	if err != nil {
		return err
	}
	rules, err := json.Marshal(src.Rules)
	if err != nil {
		return err
	}
	filt, err := json.Marshal(src.Filter)
	if err != nil {
		return err
	}
	art, err := json.Marshal(src.Article)
	if err != nil {
		return err
	}
	pol, err := json.Marshal(src.Policy)
	if err != nil {
		return err
	}
	if src.ID == 0 {
		src.CreatedAt = now
		src.UpdatedAt = now
		if src.NextRunAt == 0 {
			src.NextRunAt = now + int64(src.Policy.IntervalSec)
		}
		// 同一站点常有多个监控源（列表 + 快照 + 接口），中文名推导出的 slug
		// 容易撞车。这里自动加 -2/-3 后缀，而不是让用户先去改名字。
		base := src.Slug
		for i := 2; i < 50; i++ {
			var n int
			if err := s.db.QueryRow(`SELECT COUNT(1) FROM sources WHERE slug=?`, src.Slug).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				break
			}
			src.Slug = fmt.Sprintf("%s-%d", base, i)
		}
		res, err := s.db.Exec(`INSERT INTO sources
			(slug, name, kind, url, engine, enabled, request_json, extract_json, rules_json,
			 filter_json, article_json, policy_json, created_at, updated_at, next_run_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			src.Slug, src.Name, src.Kind, src.URL, src.Engine, src.Enabled,
			string(req), string(ext), string(rules), string(filt), string(art), string(pol),
			src.CreatedAt, src.UpdatedAt, src.NextRunAt)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		src.ID = id
		return nil
	}
	src.UpdatedAt = now
	_, err = s.db.Exec(`UPDATE sources SET slug=?, name=?, kind=?, url=?, engine=?, enabled=?,
		request_json=?, extract_json=?, rules_json=?, filter_json=?, article_json=?, policy_json=?,
		updated_at=?, next_run_at=? WHERE id=?`,
		src.Slug, src.Name, src.Kind, src.URL, src.Engine, src.Enabled,
		string(req), string(ext), string(rules), string(filt), string(art), string(pol),
		src.UpdatedAt, src.NextRunAt, src.ID)
	return err
}

// DeleteSource 删除源及其条目。
func (s *Store) DeleteSource(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sources WHERE id = ?`, id)
	return err
}

// DueSources 返回 next_run_at <= now 的启用源。
func (s *Store) DueSources(now int64) ([]model.Source, error) {
	rows, err := s.db.Query(`SELECT `+sourceCols+` FROM sources
		WHERE enabled = 1 AND next_run_at <= ? ORDER BY next_run_at LIMIT 32`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// MarkRun 记录一次运行的结果并推算下次运行时间。
func (s *Store) MarkRun(id int64, at int64, nextAt int64, failures int, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE sources SET last_run_at=?, next_run_at=?, consecutive_failures=? WHERE id=?`,
		at, nextAt, failures, id)
	return err
}

// Claim 原子地把到期源的 next_run_at 推到 leaseUntil，抢到返回 true。
// 这是跨进程租约：两个实例共用一个库时，只有 UPDATE 命中 1 行的那次会真正执行。
func (s *Store) Claim(id int64, now, leaseUntil int64, requireDue bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := `UPDATE sources SET next_run_at=? WHERE id=? AND enabled=1`
	args := []any{leaseUntil, id}
	if requireDue {
		q += ` AND next_run_at<=?`
		args = append(args, now)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// NextDue 返回最近一个到期时刻；没有启用的源时返回 0。
func (s *Store) NextDue() (int64, error) {
	var v sql.NullInt64
	err := s.db.QueryRow(`SELECT MIN(next_run_at) FROM sources WHERE enabled=1`).Scan(&v)
	if err != nil || !v.Valid {
		return 0, err
	}
	return v.Int64, nil
}

// AppendRunWarnings 把通知阶段的告警追加进已收尾的运行记录。
func (s *Store) AppendRunWarnings(runID int64, extra []string) error {
	if runID == 0 || len(extra) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if err := s.db.QueryRow(`SELECT COALESCE(warnings_json,'[]') FROM runs WHERE id=?`, runID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var list []string
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	list = append(list, extra...)
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE runs SET warnings_json=? WHERE id=?`, string(b), runID)
	return err
}

// Stats 是首页概览用的汇总计数。
type Stats struct {
	Sources   int   `json:"sources"`
	Enabled   int   `json:"enabled"`
	Entries   int   `json:"entries"`
	NewToday  int   `json:"new_today"`
	Errors24h int   `json:"errors_24h"`
	EmptyRuns int   `json:"empty_runs_24h"`
	LastRunAt int64 `json:"last_run_at"`
	NextRunAt int64 `json:"next_run_at"`
	Targets   int   `json:"targets"`
}

// GetStats 聚合概览数据。
func (s *Store) GetStats(now int64) (Stats, error) {
	var st Stats
	dayAgo := now - 86400
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(1) FROM sources),
		(SELECT COUNT(1) FROM sources WHERE enabled=1),
		(SELECT COUNT(1) FROM entries),
		(SELECT COUNT(1) FROM entries WHERE first_seen_at >= ?),
		(SELECT COUNT(1) FROM runs WHERE status='error' AND started_at >= ?),
		(SELECT COUNT(1) FROM runs WHERE status='empty' AND started_at >= ?),
		(SELECT COALESCE(MAX(started_at),0) FROM runs),
		(SELECT COALESCE(MIN(next_run_at),0) FROM sources WHERE enabled=1),
		(SELECT COUNT(1) FROM targets)`,
		dayAgo, dayAgo, dayAgo).Scan(
		&st.Sources, &st.Enabled, &st.Entries, &st.NewToday,
		&st.Errors24h, &st.EmptyRuns, &st.LastRunAt, &st.NextRunAt, &st.Targets)
	return st, err
}

// ---------------------------------------------------------------- entries

// UpsertEntry 写入条目；返回 (新增, 更新)。新增时把自增 id 回填到 e.ID，
// 通知层需要用 id 标记「已通知」。
func (s *Store) UpsertEntry(e *model.Entry, mode string) (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		existingID   int64
		existingHash string
	)
	err := s.db.QueryRow(`SELECT id, COALESCE(content_hash,'') FROM entries WHERE source_id=? AND entry_key=?`,
		e.SourceID, e.EntryKey).Scan(&existingID, &existingHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := s.db.Exec(`INSERT INTO entries
			(source_id, entry_key, guid, title, link, summary, content_html, content_text,
			 category, published_at, first_seen_at, updated_at, content_hash)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.SourceID, e.EntryKey, e.GUID, e.Title, e.Link, e.Summary, e.ContentHTML, e.ContentText,
			e.Category, e.PublishedAt, e.FirstSeenAt, e.UpdatedAt, e.ContentHash)
		if err != nil {
			return false, false, err
		}
		e.ID, _ = res.LastInsertId()
		return true, false, nil
	case err != nil:
		return false, false, err
	}

	if existingHash == e.ContentHash {
		return false, false, nil
	}
	// 空哈希表示尚未建立基线（升级迁移后首次运行，或历史数据缺失）。
	// 此时只补哈希，不算内容变化，避免一次性把全部条目判成「已更新」。
	if existingHash == "" || mode == "ignore" {
		// ignore 策略同样只记录「这一版已见过」，不改内容也不报更新，
		// 否则每次运行都会拿旧哈希反复比较。
		_, err := s.db.Exec(`UPDATE entries SET content_hash=? WHERE id=?`, e.ContentHash, existingID)
		e.ID = existingID
		return false, false, err
	}
	srcID := e.SourceID
	if mode == "archive" {
		// 版本留存：换一个 key 让新旧版本同时出现在订阅里
		e.EntryKey = fmt.Sprintf("%s@%d", e.EntryKey, s.now().UnixNano())
		e.ID = 0
		res, err := s.db.Exec(`INSERT INTO entries
			(source_id, entry_key, guid, title, link, summary, content_html, content_text,
			 category, published_at, first_seen_at, updated_at, content_hash)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			srcID, e.EntryKey, e.GUID, e.Title, e.Link, e.Summary, e.ContentHTML, e.ContentText,
			e.Category, e.PublishedAt, e.FirstSeenAt, e.UpdatedAt, e.ContentHash)
		if err != nil {
			return false, false, err
		}
		e.ID, _ = res.LastInsertId()
		return true, false, nil
	}
	_, err = s.db.Exec(`UPDATE entries SET title=?, link=?, summary=?, content_html=?,
		content_text=?, category=?, published_at=?, updated_at=?, content_hash=? WHERE id=?`,
		e.Title, e.Link, e.Summary, e.ContentHTML, e.ContentText, e.Category,
		e.PublishedAt, e.UpdatedAt, e.ContentHash, existingID)
	e.ID = existingID
	return false, true, err
}

// Entries 返回某源的条目，最新在前。
func (s *Store) Entries(sourceID int64, limit, offset int) ([]model.Entry, error) {
	rows, err := s.db.Query(`SELECT id, source_id, entry_key, guid, COALESCE(title,''), COALESCE(link,''),
		COALESCE(summary,''), COALESCE(content_html,''), COALESCE(content_text,''), COALESCE(category,''),
		COALESCE(published_at,0), first_seen_at, COALESCE(updated_at,0), COALESCE(content_hash,''), COALESCE(notified_at,0)
		FROM entries WHERE source_id=? ORDER BY COALESCE(published_at, first_seen_at) DESC LIMIT ? OFFSET ?`,
		sourceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Entry
	for rows.Next() {
		var e model.Entry
		if err := rows.Scan(&e.ID, &e.SourceID, &e.EntryKey, &e.GUID, &e.Title, &e.Link,
			&e.Summary, &e.ContentHTML, &e.ContentText, &e.Category,
			&e.PublishedAt, &e.FirstSeenAt, &e.UpdatedAt, &e.ContentHash, &e.NotifiedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HasEntry 判断某条是否已存在（用于决定是否值得抓详情页）。
func (s *Store) HasEntry(sourceID int64, entryKey string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE source_id=? AND entry_key=?`,
		sourceID, entryKey).Scan(&n)
	return n > 0, err
}

// CountEntries 统计某源条目数。
func (s *Store) CountEntries(sourceID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE source_id=?`, sourceID).Scan(&n)
	return n, err
}

// DeleteEntry 删除单条。
func (s *Store) DeleteEntry(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM entries WHERE id=?`, id)
	return err
}

// MarkNotified 记录已通知时间。
func (s *Store) MarkNotified(ids []int64, at int64) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE entries SET notified_at=? WHERE id=?`, at, id); err != nil {
			return err
		}
	}
	return nil
}

// Prune 按保留期清理旧条目。
func (s *Store) Prune(sourceID int64, cutoff int64) error {
	if cutoff <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM entries WHERE source_id=? AND COALESCE(published_at, first_seen_at) < ?`,
		sourceID, cutoff)
	return err
}

// ---------------------------------------------------------------- snapshots

// Snapshot 是一次 page 型快照。
type Snapshot struct {
	Hash       string `json:"hash"`
	Text       string `json:"text"`
	CapturedAt int64  `json:"captured_at"`
}

// GetSnapshot 读取快照，不存在时返回零值。
func (s *Store) GetSnapshot(sourceID int64) (Snapshot, error) {
	var snap Snapshot
	err := s.db.QueryRow(`SELECT hash, text, captured_at FROM snapshots WHERE source_id=?`, sourceID).
		Scan(&snap.Hash, &snap.Text, &snap.CapturedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, nil
	}
	return snap, err
}

// PutSnapshot 覆盖快照。
func (s *Store) PutSnapshot(sourceID int64, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO snapshots(source_id, hash, text, captured_at) VALUES (?,?,?,?)
		ON CONFLICT(source_id) DO UPDATE SET hash=excluded.hash, text=excluded.text, captured_at=excluded.captured_at`,
		sourceID, snap.Hash, snap.Text, snap.CapturedAt)
	return err
}

// ---------------------------------------------------------------- runs

// StartRun 开一条 running 记录。
func (s *Store) StartRun(sourceID int64, trigger string, at int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO runs(source_id, started_at, status, trigger) VALUES (?,?,?,?)`,
		sourceID, at, "running", trigger)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun 收尾一条运行记录。
func (s *Store) FinishRun(r model.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	warn, err := json.Marshal(r.Warnings)
	if err != nil {
		warn = []byte("[]")
	}
	_, err = s.db.Exec(`UPDATE runs SET finished_at=?, status=?, items_found=?, items_new=?,
		items_updated=?, duration_ms=?, error=?, warnings_json=? WHERE id=?`,
		r.FinishedAt, r.Status, r.ItemsFound, r.ItemsNew, r.ItemsUpdated, r.DurationMS, r.Error, string(warn), r.ID)
	return err
}

// ListRuns 返回某源最近运行记录；sourceID=0 时返回全局。
func (s *Store) ListRuns(sourceID int64, limit int) ([]model.Run, error) {
	q := `SELECT id, source_id, started_at, COALESCE(finished_at,0), status, COALESCE(trigger,''),
		items_found, items_new, items_updated, duration_ms, COALESCE(error,''), COALESCE(warnings_json,'[]')
		FROM runs`
	args := []any{}
	if sourceID > 0 {
		q += ` WHERE source_id=?`
		args = append(args, sourceID)
	}
	q += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Run
	for rows.Next() {
		var (
			r    model.Run
			warn string
		)
		if err := rows.Scan(&r.ID, &r.SourceID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Trigger,
			&r.ItemsFound, &r.ItemsNew, &r.ItemsUpdated, &r.DurationMS, &r.Error, &warn); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(warn), &r.Warnings)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentStatuses 返回某源最近若干次已结束运行的状态，新的在前。
// 用于识别 recovery（上次失败本次成功）与连续空跑（静默失效）。
func (s *Store) RecentStatuses(sourceID int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.Query(`SELECT status FROM runs
		WHERE source_id=? AND status <> 'running' ORDER BY started_at DESC, id DESC LIMIT ?`,
		sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// InterruptRunning 把残留的 running 标记为中断。
func (s *Store) InterruptRunning(at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE runs SET status='interrupted', finished_at=? WHERE status='running'`, at)
	return err
}

// Running 判断某源是否有未结束的运行（租约）。
func (s *Store) Running(sourceID int64, since int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM runs WHERE source_id=? AND status='running' AND started_at >= ?`,
		sourceID, since).Scan(&n)
	return n > 0, err
}

// ---------------------------------------------------------------- targets

// ListTargets 返回通知目标（URL 已解密）。
func (s *Store) ListTargets(box *secretbox.Box) ([]model.Target, error) {
	rows, err := s.db.Query(`SELECT id, name, url_enc, COALESCE(kind,''), enabled, created_at FROM targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Target
	for rows.Next() {
		var (
			t   model.Target
			enc []byte
		)
		if err := rows.Scan(&t.ID, &t.Name, &enc, &t.Kind, &t.Enabled, &t.CreatedAt); err != nil {
			return nil, err
		}
		if plain, err := box.Open(enc); err == nil {
			t.URL = plain
		}
		t.Masked = maskURL(t.URL)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTarget 新增或更新目标。
func (s *Store) SaveTarget(t *model.Target, box *secretbox.Box) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := box.Seal(t.URL)
	if err != nil {
		return err
	}
	if t.ID == 0 {
		if t.CreatedAt == 0 {
			t.CreatedAt = s.now().Unix()
		}
		res, err := s.db.Exec(`INSERT INTO targets(name, url_enc, kind, enabled, created_at) VALUES (?,?,?,?,?)`,
			t.Name, enc, t.Kind, t.Enabled, t.CreatedAt)
		if err != nil {
			return err
		}
		t.ID, err = res.LastInsertId()
		return err
	}
	_, err = s.db.Exec(`UPDATE targets SET name=?, url_enc=?, kind=?, enabled=? WHERE id=?`,
		t.Name, enc, t.Kind, t.Enabled, t.ID)
	return err
}

// DeleteTarget 删除目标。
func (s *Store) DeleteTarget(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM targets WHERE id=?`, id)
	return err
}

// TargetURLs 返回指定 id 集合的 URL（用于发送）。
func (s *Store) TargetURLs(ids []int64, box *secretbox.Box) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	all, err := s.ListTargets(box)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []string
	for _, t := range all {
		if want[t.ID] && t.Enabled && t.URL != "" {
			out = append(out, t.URL)
		}
	}
	return out, nil
}

func maskURL(u string) string {
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		scheme, rest := u[:i+3], u[i+3:]
		if len(rest) <= 12 {
			return scheme + "****"
		}
		return scheme + rest[:6] + "…" + rest[len(rest)-4:]
	}
	if len(u) <= 12 {
		return "****"
	}
	return u[:6] + "…" + u[len(u)-4:]
}

// ---------------------------------------------------------------- settings

// GetSetting 读设置。
func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting 写设置。
func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

const appSettingsKey = "app_settings"

// GetSettings 读实例设置；从未保存过时返回内置默认值。
func (s *Store) GetSettings() (model.Settings, error) {
	out := model.DefaultSettings()
	raw := s.GetSetting(appSettingsKey, "")
	if raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return model.DefaultSettings(), fmt.Errorf("解析设置: %w", err)
	}
	out.Normalize()
	return out, nil
}

// SaveSettings 保存实例设置。
func (s *Store) SaveSettings(v model.Settings) error {
	v.Normalize()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SetSetting(appSettingsKey, string(b))
}
