// Package scheduler 负责周期触发：数据库里的 next_run_at 是唯一真相源，
// 循环按「最近一个到期时刻」决定睡多久（等价于最小堆调度，且天然支持多实例）。
// 每次执行前用一条原子 UPDATE 抢占租约，避免同一源被并发跑。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/Felix2yu/siwu/internal/cronexpr"
	"github.com/Felix2yu/siwu/internal/events"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/pipeline"
	"github.com/Felix2yu/siwu/internal/store"
)

// ErrBusy 表示该源已在运行中。
var ErrBusy = errors.New("该源正在运行，请稍候")

const (
	maxBackoff = int64(3600) // 失败退避上限 1h
	minLease   = 30 * time.Second
	maxLease   = 15 * time.Minute
)

// Scheduler 负责周期触发。
type Scheduler struct {
	Deps    *pipeline.Deps
	Store   *store.Store
	Log     *slog.Logger
	Bus     *events.Bus
	Workers int
	Tick    time.Duration // 最长睡眠时间（保证新增源及时被看到）

	inflight sync.Map // sourceID -> struct{}
	wake     chan struct{}
}

// New 构造调度器。
func New(deps *pipeline.Deps, st *store.Store, log *slog.Logger, workers int) *Scheduler {
	if workers <= 0 {
		workers = 4
	}
	return &Scheduler{
		Deps: deps, Store: st, Log: log, Workers: workers,
		Tick: 30 * time.Second, wake: make(chan struct{}, 1),
	}
}

// Start 阻塞运行，直到 ctx 结束。
func (s *Scheduler) Start(ctx context.Context) {
	sem := make(chan struct{}, s.Workers)
	go s.loop(ctx, sem)
	<-ctx.Done()
	// 等待在跑的 goroutine 收尾
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		s.inflight.Range(func(_, _ any) bool { busy = true; return false })
		if !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Kick 立即触发一轮扫描（新建/修改源后调用）。
func (s *Scheduler) Kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) loop(ctx context.Context, sem chan struct{}) {
	for {
		s.dispatchDue(ctx, sem)

		timer := time.NewTimer(s.nextWait())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// nextWait 睡到最近一个到期时刻，但不超过 Tick，也不短于 2s（防止忙等）。
func (s *Scheduler) nextWait() time.Duration {
	wait := s.Tick
	if wait <= 0 {
		wait = 30 * time.Second
	}
	due, err := s.Store.NextDue()
	if err != nil || due == 0 {
		return wait
	}
	d := time.Until(time.Unix(due, 0))
	if d < 2*time.Second {
		return 2 * time.Second
	}
	if d < wait {
		return d
	}
	return wait
}

func (s *Scheduler) dispatchDue(ctx context.Context, sem chan struct{}) {
	now := time.Now()
	due, err := s.Store.DueSources(now.Unix())
	if err != nil {
		s.Log.Error("查询到期源失败", "err", err)
		return
	}
	for _, src := range due {
		// 原子抢占：多实例共库时只有一个实例能拿到
		ok, err := s.Store.Claim(src.ID, now.Unix(), now.Add(s.leaseFor(src)).Unix(), true)
		if err != nil {
			s.Log.Error("抢占租约失败", "source", src.Slug, "err", err)
			continue
		}
		if !ok {
			continue
		}
		if _, running := s.inflight.Load(src.ID); running {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(src model.Source) {
			defer func() {
				<-sem
			}()
			if _, err := s.RunSource(ctx, src, "schedule"); err != nil && !errors.Is(err, ErrBusy) {
				s.Log.Warn("定时运行失败", "source", src.Slug, "err", err)
			}
		}(src)
	}
}

// leaseFor 决定租约时长：不超过一个调度间隔的一半，避免压掉下一次正常触发。
// 执行结束会立刻用 MarkRun 覆盖，所以短租约不影响正确性，只影响崩溃后的恢复延迟。
func (s *Scheduler) leaseFor(src model.Source) time.Duration {
	lease := maxLease
	if src.Policy.IntervalSec > 0 {
		half := time.Duration(src.Policy.IntervalSec) * time.Second / 2
		if half < lease {
			lease = half
		}
	}
	if lease < minLease {
		lease = minLease
	}
	return lease
}

// RunSource 执行一次并把结果写回调度字段。手动触发也走这里，保证互斥与退避一致。
func (s *Scheduler) RunSource(ctx context.Context, src model.Source, trigger string) (pipeline.Outcome, error) {
	if _, loaded := s.inflight.LoadOrStore(src.ID, struct{}{}); loaded {
		return pipeline.Outcome{}, ErrBusy
	}
	defer s.inflight.Delete(src.ID)

	now := time.Now()
	// 手动触发也先推后 next_run_at，避免定时器紧跟着再跑一遍
	if trigger != "schedule" {
		if _, err := s.Store.Claim(src.ID, now.Unix(), now.Add(s.leaseFor(src)).Unix(), false); err != nil {
			s.Log.Warn("手动运行未能更新租约", "source", src.Slug, "err", err)
		}
	}
	if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type: "run.start", SourceID: src.ID, SourceName: src.Name, Slug: src.Slug,
			Status: "running",
		})
	}

	out, err := s.Deps.Execute(ctx, src, trigger, false)
	finished := time.Now()

	next, failures, planWarns := planNext(src, finished, err != nil)
	out.Warnings = append(out.Warnings, planWarns...)

	errText := ""
	if err != nil {
		errText = err.Error()
	}
	if !src.Enabled && trigger == "schedule" {
		// 停用后不再排下一次，但仍记录本次结果
		next = time.Unix(0, 0)
	}
	if markErr := s.Store.MarkRun(src.ID, finished.Unix(), next.Unix(), failures, errText); markErr != nil {
		s.Log.Error("更新调度字段失败", "source", src.Slug, "err", markErr)
	}
	s.Log.Info("运行完成", "source", src.Slug, "trigger", trigger, "status", out.Run.Status,
		"found", out.ItemsFound, "new", len(out.NewEntries), "updated", out.Updated, "err", errText)

	if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type: "run.finish", SourceID: src.ID, SourceName: src.Name, Slug: src.Slug,
			RunID: out.Run.ID, Status: out.Run.Status, Found: out.ItemsFound,
			New: len(out.NewEntries), Updated: out.Updated,
			DurationMS: out.Run.DurationMS, Error: errText,
		})
	}
	return out, err
}

// planNext 计算下次触发时刻与新的连续失败次数。
//
// 规则：cron 优先；失败按 60s×2^(n-1) 指数退避（上限 1h，且不晚于 cron 的下次时刻）；
// 固定间隔加 ±10% 抖动，cron 按日历精确触发。
func planNext(src model.Source, finished time.Time, failed bool) (time.Time, int, []string) {
	interval := int64(src.Policy.IntervalSec)
	if interval <= 0 {
		interval = 3600
	}
	var warns []string
	cronSch, hasCron, cronErr := cronOf(src)
	if cronErr != nil {
		warns = append(warns, cronErr.Error())
	}

	if !failed {
		if hasCron {
			if n := cronSch.Next(finished); !n.IsZero() {
				return n, 0, warns
			}
		}
		return finished.Add(time.Duration(withJitter(interval)) * time.Second), 0, warns
	}

	failures := src.Failures + 1
	backoff := 60 * int64(1) << uint(min(failures-1, 6))
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	cand := finished.Add(time.Duration(withJitter(backoff)) * time.Second)
	if hasCron {
		// 失败时退避可以提前探活，但没必要晚于正常的下次时刻
		if cn := cronSch.Next(finished); !cn.IsZero() && cand.After(cn) {
			cand = cn
		}
	} else {
		capSec := interval
		if capSec > maxBackoff {
			capSec = maxBackoff
		}
		if backoff >= capSec {
			// 退避不必超过正常间隔
			cand = finished.Add(time.Duration(withJitter(capSec)) * time.Second)
		}
	}
	if cand.Before(finished.Add(30 * time.Second)) {
		cand = finished.Add(30 * time.Second)
	}
	return cand, failures, warns
}

func cronOf(src model.Source) (cronexpr.Schedule, bool, error) {
	expr := strings.TrimSpace(src.Policy.Cron)
	if expr == "" {
		return cronexpr.Schedule{}, false, nil
	}
	sch, err := cronexpr.Parse(expr)
	if err != nil {
		return cronexpr.Schedule{}, false, fmt.Errorf("cron 表达式无效，已退回固定间隔: %v", err)
	}
	return sch, true, nil
}

// withJitter 给间隔加 ±10% 抖动，避免同批源在同一秒集中抓取。
func withJitter(sec int64) int64 {
	if sec <= 0 {
		return sec
	}
	j := int64(float64(sec) * (rand.Float64()*0.2 - 0.1))
	out := sec + j
	if out < 30 {
		out = 30
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
