package extract

import (
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

const listHTML = `<html><body>
<div class="content-outer list"><div class="content"><ul>
<li><a href="news-detail.aspx?id=1">苏州太湖音乐喷泉开放公告 <span class="tag">NEW</span></a><div class="date"><div>2026-10-16</div></div></li>
<li><a href="/news-detail.aspx?id=2">大阳山景区重阳节当日免票入园</a><div class="date"><div>2026-10-15</div></div></li>
</ul></div></div></body></html>`

func TestHTMLExtractsItemsWithXPath(t *testing.T) {
	spec := model.Extract{
		Item: "xpath://div[contains(@class,'content-outer')]//ul/li",
		Fields: map[string]model.Field{
			"title": {Sel: "css:a"},
			"link":  {Sel: "css:a", Attr: "href", Absolute: true},
			"date":  {Sel: "css:.date > div"},
		},
	}
	rules := []model.Rule{{
		ID: "strip-new", Target: []string{"title"}, Op: "regex_replace",
		Pattern: `\s*NEW\s*$`, Replace: "",
	}}
	res, err := HTML([]byte(listHTML), "https://visitsz.wglj.suzhou.com.cn/news.aspx", spec, rules)
	if err != nil {
		t.Fatalf("抽取失败: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(res.Items))
	}
	if got := strings.TrimSpace(res.Items[0].Title); got != "苏州太湖音乐喷泉开放公告" {
		t.Errorf("标题清洗后应为「苏州太湖音乐喷泉开放公告」，实际 %q", got)
	}
	if got := res.Items[1].Link; got != "https://visitsz.wglj.suzhou.com.cn/news-detail.aspx?id=2" {
		t.Errorf("相对链接未补全: %q", got)
	}
	if res.Items[0].Date != "2026-10-16" {
		t.Errorf("日期取值错误: %q", res.Items[0].Date)
	}
}

func TestHTMLZeroMatchWarns(t *testing.T) {
	spec := model.Extract{Item: "css:.not-exist",
		Fields: map[string]model.Field{"title": {Sel: "a"}}}
	res, err := HTML([]byte(listHTML), "", spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 || len(res.Warnings) == 0 {
		t.Fatalf("0 命中时必须给出警告，实际 items=%d warnings=%v", len(res.Items), res.Warnings)
	}
}

func TestJSONPathTemplate(t *testing.T) {
	raw := []byte(`{"code":0,"data":[
		{"ArtId":1326,"ArtName":"扬剧《百岁挂帅》","StartTime":"2026-10-18 18:30:00","ArtTypeName":"演出类","ArtContent":"<p>正文</p>"},
		{"ArtId":1327,"ArtName":"话剧","StartTime":"2026-10-19 19:30:00","ArtTypeName":"演出类","ArtContent":""}
	]}`)
	spec := model.Extract{
		Item: "$.data",
		Fields: map[string]model.Field{
			"title":    {Sel: "$.ArtName"},
			"link":     {Template: "https://suda.meiyuedu.cn/web/ArtDetail?ArtId={$.ArtId}"},
			"date":     {Sel: "$.StartTime"},
			"category": {Sel: "$.ArtTypeName"},
			"content":  {Sel: "$.ArtContent"},
		},
	}
	res, err := JSON(raw, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(res.Items))
	}
	if res.Items[0].Link != "https://suda.meiyuedu.cn/web/ArtDetail?ArtId=1326" {
		t.Errorf("模板插值失败: %q", res.Items[0].Link)
	}
	if res.Items[0].Category != "演出类" {
		t.Errorf("分类取值失败: %q", res.Items[0].Category)
	}
}

func TestJSONPathInvalid(t *testing.T) {
	if _, err := Parse("json:data.list"); err == nil {
		t.Fatal("不带 $ 的 JSON 路径应当报错")
	}
}

func TestSnapshotIgnoresNoise(t *testing.T) {
	html := `<html><body><div id="main"><p>公告一</p><p>浏览量：123</p><script>x()</script></div></body></html>`
	spec := model.Extract{Scope: "xpath://div[@id='main']", IgnoreRegex: []string{`浏览量：\d+`}}
	text, _, warns, err := Snapshot([]byte(html), "", spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "浏览量") {
		t.Errorf("噪声未被剔除: %q", text)
	}
	if !strings.Contains(text, "公告一") {
		t.Errorf("正文缺失: %q", text)
	}
	if len(warns) != 0 {
		t.Errorf("不该有警告: %v", warns)
	}
}
