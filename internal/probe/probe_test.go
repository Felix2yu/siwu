package probe

import (
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

func TestArticleUsesRawHTML(t *testing.T) {
	raw := []byte(`<html><head><title>详情</title></head><body>
<div id="content"><p>预览里的正文</p><script>alert(1)</script></div>
</body></html>`)
	p := &Probe{sessions: map[string]*Session{
		"sid": {ID: "sid", URL: "https://example.com/a", Raw: raw},
	}}
	res, err := p.Article("sid", model.Article{Content: "css:#content", Readability: true})
	if err != nil {
		t.Fatalf("正文抽取失败: %v", err)
	}
	if res.Method != "selector" || !strings.Contains(res.ContentHTML, "预览里的正文") {
		t.Fatalf("结果不对: %+v", res)
	}
	if strings.Contains(res.ContentHTML, "alert") {
		t.Fatalf("净化没生效: %s", res.ContentHTML)
	}
}

func TestArticleExpiredSession(t *testing.T) {
	p := &Probe{sessions: map[string]*Session{}}
	if _, err := p.Article("gone", model.Article{Readability: true}); err == nil {
		t.Fatal("过期会话应报错")
	}
}
