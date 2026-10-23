package transform

import (
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

func TestStripTrailingNEW(t *testing.T) {
	rules := []model.Rule{
		{ID: "strip-new", Target: []string{"title"}, Op: "regex_replace",
			Pattern: `\s*[\[【(（]?NEW[\]】)）]?\s*$`, Replace: "", Flags: "i"},
	}
	cases := map[string]string{
		"苏州博物馆闭馆通知 NEW":  "苏州博物馆闭馆通知",
		"苏州博物馆闭馆通知【NEW】": "苏州博物馆闭馆通知",
		"苏州博物馆闭馆通知 new":  "苏州博物馆闭馆通知",
		"苏州博物馆闭馆通知":      "苏州博物馆闭馆通知",
	}
	for in, want := range cases {
		if got := Apply("title", in, rules); got != want {
			t.Errorf("Apply(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestDateParseCN(t *testing.T) {
	rule := model.Rule{ID: "d", Op: "date_parse", Layouts: []string{"2006-01-02"}, TZ: "Asia/Shanghai"}
	got := Apply("date", "2026-10-16", []model.Rule{rule})
	if got != "2026-10-16T00:00:00+08:00" {
		t.Fatalf("日期归一结果异常: %q", got)
	}
	if ts := ParseDate(got); ts == 0 {
		t.Fatal("归一后的日期应能被解析")
	}
}

func TestInvalidRegexRejected(t *testing.T) {
	_, err := Compile(model.Rule{ID: "bad", Op: "regex_replace", Pattern: "([a-z"})
	if err == nil {
		t.Fatal("非法正则应当在保存时报错")
	}
}

func TestRemoveTags(t *testing.T) {
	got := RemoveTags("<p>一</p><script>bad()</script><p>二</p>")
	if got != "一 二" {
		t.Fatalf("去标签结果异常: %q", got)
	}
}

// 只有「一个确定的时间点」才配叫发布时间：带年份、能解析、不是区间。
func TestLooksLikeDate(t *testing.T) {
	yes := []string{
		"2026-10-16", "2026-10-16 08:30", "2026-10-16 08:30:05",
		"2026-10-16T08:30:00+08:00", "2026/10/16", "2026.10.16", "2026年10月16日",
	}
	no := []string{
		"", "10-18 18:30", "10-02 18:30—10-02 21:00", "2026-10-16 18:30—2026-10-16 21:00",
		"2026-10-09 09:32 至 2026-10-18 18:30", "主办单位：艺术教育中心",
	}
	for _, s := range yes {
		if !LooksLikeDate(s) {
			t.Errorf("应当认作日期: %q", s)
		}
		if ParseDate(s) == 0 {
			t.Errorf("ParseDate 解析不了 %q", s)
		}
	}
	for _, s := range no {
		if LooksLikeDate(s) {
			t.Errorf("不该认作日期: %q", s)
		}
	}
}
