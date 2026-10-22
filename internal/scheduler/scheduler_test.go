package scheduler

import (
	"testing"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
)

func srcWith(interval int, cron string, failures int) model.Source {
	return model.Source{Policy: model.Policy{IntervalSec: interval, Cron: cron}, Failures: failures}
}

func within(t *testing.T, got time.Time, from time.Time, lo, hi time.Duration, what string) {
	t.Helper()
	d := got.Sub(from)
	if d < lo || d > hi {
		t.Fatalf("%s：得到 %s（%.0fs 后），期望 %s~%s", what, got, d.Seconds(), lo, hi)
	}
}

func TestPlanNextIntervalSuccess(t *testing.T) {
	now := time.Now()
	next, failures, warns := planNext(srcWith(600, "", 0), now, false)
	if failures != 0 || len(warns) != 0 {
		t.Fatalf("成功运行不应有失败计数/告警: %d %v", failures, warns)
	}
	within(t, next, now, 540*time.Second, 660*time.Second, "固定间隔成功")
}

func TestPlanNextCronSuccess(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 20, 0, 0, time.UTC)
	next, _, _ := planNext(srcWith(9999, "0 * * * *", 0), now, false)
	want := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("cron 应精确到下一个整点：得到 %s，期望 %s", next, want)
	}
}

func TestPlanNextBackoff(t *testing.T) {
	now := time.Now()
	// 第一次失败：60s ±10%（不低于 30s）
	next, failures, _ := planNext(srcWith(3600, "", 0), now, true)
	if failures != 1 {
		t.Fatalf("失败计数应为 1，得到 %d", failures)
	}
	within(t, next, now, 54*time.Second, 66*time.Second, "首次失败退避")

	// 连续第 5 次失败：60×2^4 = 960s ±10%
	next, failures, _ = planNext(srcWith(7200, "", 4), now, true)
	if failures != 5 {
		t.Fatalf("失败计数应为 5，得到 %d", failures)
	}
	within(t, next, now, 864*time.Second, 1056*time.Second, "第 5 次失败退避")
}

func TestPlanNextBackoffCappedByInterval(t *testing.T) {
	now := time.Now()
	// 间隔只有 120s：退避不能超过正常间隔
	next, _, _ := planNext(srcWith(120, "", 6), now, true)
	within(t, next, now, 108*time.Second, 132*time.Second, "退避受间隔上限约束")
}

func TestPlanNextCronNotPostponed(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 55, 0, 0, time.UTC)
	// cron 下次是 11:00（5 分钟后），退避 60s 更早，应取更早的探活时间
	next, _, _ := planNext(srcWith(3600, "0 * * * *", 0), now, true)
	if !next.Before(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("失败退避不应晚于 cron 的下次时刻，得到 %s", next)
	}
}

func TestPlanNextBadCronFallsBack(t *testing.T) {
	now := time.Now()
	next, _, warns := planNext(srcWith(600, "bad cron", 0), now, false)
	if len(warns) == 0 {
		t.Fatal("cron 非法时应给出告警")
	}
	within(t, next, now, 540*time.Second, 660*time.Second, "退回固定间隔")
}

func TestLeaseBounds(t *testing.T) {
	s := &Scheduler{}
	if got := s.leaseFor(srcWith(120, "", 0)); got != 60*time.Second {
		t.Fatalf("120s 间隔的租约应为 60s，得到 %s", got)
	}
	if got := s.leaseFor(srcWith(3600, "", 0)); got != maxLease {
		t.Fatalf("长间隔租约应封顶 %s，得到 %s", maxLease, got)
	}
	if got := s.leaseFor(srcWith(10, "", 0)); got != minLease {
		t.Fatalf("极短间隔租约应兜底 %s，得到 %s", minLease, got)
	}
}
