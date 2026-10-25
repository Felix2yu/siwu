package model

import "testing"

func TestClampInterval(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultIntervalSec},
		{-5, DefaultIntervalSec},
		{1, MinIntervalSec},
		{1800, 1800},
		{7 * 24 * 3600, 7 * 24 * 3600},
	}
	for _, c := range cases {
		if got := ClampInterval(c.in); got != c.want {
			t.Fatalf("ClampInterval(%d) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

func TestDriftWindowIsMinuteAligned(t *testing.T) {
	cases := []struct{ period, max, want int }{
		{1800, MaxIntervalDriftSec, 180},
		{3600, MaxIntervalDriftSec, 360},
		{86400, MaxIntervalDriftSec, MaxIntervalDriftSec},
		{300, MaxIntervalDriftSec, 0}, // 不足 1 分钟，不抖
		{0, MaxIntervalDriftSec, 0},
	}
	for _, c := range cases {
		if got := DriftWindow(c.period, c.max); got != c.want {
			t.Fatalf("DriftWindow(%d,%d) = %d，期望 %d", c.period, c.max, got, c.want)
		}
	}
}

func TestDriftSecStaysInWindow(t *testing.T) {
	w := DriftWindow(1800, MaxIntervalDriftSec)
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		got := DriftSec(1800, MaxIntervalDriftSec)
		if got < -w || got > w || got%60 != 0 {
			t.Fatalf("漂移 %d 超出 ±%d 或不是整分钟", got, w)
		}
		seen[got] = true
	}
	if len(seen) < 3 {
		t.Fatalf("漂移取值过于集中: %v", seen)
	}
}

func TestDelaySecOnlyPostpones(t *testing.T) {
	w := DriftWindow(3600, MaxCalendarDriftSec)
	for i := 0; i < 200; i++ {
		got := DelaySec(3600, MaxCalendarDriftSec)
		if got < 0 || got > w || got%60 != 0 {
			t.Fatalf("延后 %d 超出 [0,%d] 或不是整分钟", got, w)
		}
	}
	// 窗口为 0 时必须原样返回，日历时刻不能被动
	if got := DelaySec(300, MaxCalendarDriftSec); got != 0 {
		t.Fatalf("短周期不该延后，得到 %d", got)
	}
}

func TestPolicyDefaultsUseMinuteInterval(t *testing.T) {
	s := Source{}
	s.Defaults()
	if s.Policy.IntervalSec != DefaultIntervalSec {
		t.Fatalf("默认间隔应是 %d 秒（60 分钟），得到 %d", DefaultIntervalSec, s.Policy.IntervalSec)
	}
	if DefaultIntervalSec%60 != 0 || DefaultIntervalSec < 60 {
		t.Fatal("默认间隔必须是整分钟")
	}
}
