package cronexpr

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		{"*/15 * * * *", "2026-10-22 10:07", "2026-10-22 10:15"},
		{"*/15 * * * *", "2026-10-22 10:45", "2026-10-22 11:00"},
		{"0 9 * * 1-5", "2026-10-23 18:00", "2026-10-26 09:00"}, // 周五 → 下周一
		{"0 9 * * 1-5", "2026-10-24 08:00", "2026-10-26 09:00"}, // 周六 → 周一
		{"30 2 * * *", "2026-10-22 03:00", "2026-10-23 02:30"},
		{"0 0 1 * *", "2026-10-22 00:00", "2026-11-01 00:00"},
		{"0 0 29 2 *", "2026-10-22 00:00", "2028-02-29 00:00"},
		{"0,30 * * * *", "2026-10-22 10:31", "2026-10-22 11:00"},
		{"5/20 * * * *", "2026-10-22 10:00", "2026-10-22 10:05"},
		{"0 0 * * 7", "2026-10-22 00:00", "2026-10-25 00:00"}, // 7 = 周日
		{"0 0 * * 0", "2026-10-22 00:00", "2026-10-25 00:00"},
		// 日与周同时限定时取并集：既命中「1 号」也命中「周一」，
		// 所以 10-26（周一）比 11-01（1 号）先触发。
		{"0 0 1 * 1", "2026-10-22 00:00", "2026-10-26 00:00"},
		{"0 0 1 * 1", "2026-10-27 00:00", "2026-11-01 00:00"},
		{"@daily", "2026-10-22 10:00", "2026-10-23 00:00"},
		{"@hourly", "2026-10-22 10:00", "2026-10-22 11:00"},
	}
	for _, c := range cases {
		sch, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("解析 %q: %v", c.expr, err)
		}
		got := sch.Next(at(c.from))
		if got.IsZero() {
			t.Fatalf("%q 从 %s 起算返回零值", c.expr, c.from)
		}
		if got.UTC().Format("2006-01-02 15:04") != c.want {
			t.Errorf("%q 从 %s：得到 %s，期望 %s", c.expr, c.from,
				got.UTC().Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestEvery(t *testing.T) {
	sch, err := Parse("@every 90m")
	if err != nil {
		t.Fatal(err)
	}
	if !sch.IsEvery() {
		t.Fatal("@every 应标记为固定时长")
	}
	from := at("2026-10-22 10:00")
	if got := sch.Next(from); !got.Equal(from.Add(90 * time.Minute)) {
		t.Fatalf("得到 %s，期望 %s", got, from.Add(90*time.Minute))
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *",
		"* * * * 8", "*/0 * * * *", "a * * * *", "5-1 * * * *", "@every 30s",
		"@nonsense",
	}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("%q 应当报错", expr)
		}
	}
}
