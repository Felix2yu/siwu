package article

import (
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

func TestExtractUsesSelectorAndAbsolutizesLinks(t *testing.T) {
	raw := []byte(`<html><head><title>页面标题</title></head><body>
<div class="ad">广告</div>
<div id="body"><p>正文第一段</p><a href="/news/2">下一条</a></div>
</body></html>`)
	res, err := Extract(raw, "https://example.com/news/1", model.Article{
		Enabled: true, Content: "css:#body", Readability: true,
	})
	if err != nil {
		t.Fatalf("抽取失败: %v", err)
	}
	if res.Method != "selector" {
		t.Fatalf("应标记为选择器命中，实际 %q", res.Method)
	}
	if !strings.Contains(res.ContentHTML, "正文第一段") {
		t.Fatalf("正文缺失: %s", res.ContentHTML)
	}
	if !strings.Contains(res.ContentHTML, "https://example.com/news/2") {
		t.Errorf("相对链接未补全: %s", res.ContentHTML)
	}
	if strings.Contains(res.ContentHTML, "script") {
		t.Errorf("正文不应包含脚本: %s", res.ContentHTML)
	}
}

func TestExtractFallsBackToReadability(t *testing.T) {
	paragraph := strings.Repeat("这是一段用于让 readability 判断正文主体的文字。", 6)
	raw := []byte(`<html><head><title>文章标题</title></head><body>
<nav><a href="/">首页</a><a href="/list">列表</a></nav>
<article><h1>文章标题</h1>` +
		"<p>" + paragraph + "</p><p>" + paragraph + "</p><p>" + paragraph + "</p>" +
		`</article><footer>版权</footer></body></html>`)
	res, err := Extract(raw, "https://example.com/a/1", model.Article{
		Enabled: true, Content: "css:#missing", Readability: true,
	})
	if err != nil {
		t.Fatalf("readability 兜底失败: %v", err)
	}
	if res.Method != "readability" {
		t.Fatalf("应标记为 readability，实际 %q", res.Method)
	}
	if !strings.Contains(res.ContentText, "readability 判断正文主体") {
		t.Fatalf("正文缺失: %s", res.ContentText)
	}
}

func TestExtractErrorsWhenBothPathsMiss(t *testing.T) {
	raw := []byte(`<html><head><title>空页</title></head><body><p>太短</p></body></html>`)
	_, err := Extract(raw, "https://example.com/a", model.Article{
		Enabled: true, Content: "css:#missing", Readability: false,
	})
	if err == nil {
		t.Fatal("选择器未命中且关闭 readability 时应报错")
	}
}
