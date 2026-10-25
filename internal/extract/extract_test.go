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

// 模板字段必须最后渲染：link 在 fieldOrder 里排在 guid 之前，
// 但它的模板引用 {guid}。单遍处理会渲染成空串，链接被静默截断。
func TestHTMLLinkTemplateUsesLaterField(t *testing.T) {
	const doc = `<html><body><div class="list-card-item" id="filter-234972004">
<div class="title">演出 A</div><div class="time">2026.09.13-12.31</div></div></body></html>`
	spec := model.Extract{
		Item: "css:.list-card-item",
		Fields: map[string]model.Field{
			"title": {Sel: "css:.title"},
			"guid":  {Sel: "xpath:.", Attr: "id", Transforms: []string{"pid"}},
			"link":  {Template: "https://szwtfz.maitix.com/h5/#/pages-order/projectDetail/index?projectId={guid}"},
			"date":  {Sel: "css:.time"},
		},
	}
	rules := []model.Rule{{ID: "pid", Op: "strip_prefix", Value: "filter-"}}
	res, err := HTML([]byte(doc), "https://szwtfz.maitix.com/h5/", spec, rules)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 {
		t.Fatalf("应命中 1 条，实际 %d", res.Matched)
	}
	got := res.Items[0]
	// 麦座 H5 实测可用的详情路由；projectId 就是卡片 id 里的 projectToken
	if got.Link != "https://szwtfz.maitix.com/h5/#/pages-order/projectDetail/index?projectId=234972004" {
		t.Fatalf("链接模板未取到 guid：%q", got.Link)
	}
	if got.GUID != "234972004" {
		t.Fatalf("guid 应为去掉前缀的 id，实际 %q", got.GUID)
	}
}

// 字段推断给条目根取链接/标题时会写 sel="."，这个写法必须按 XPath 的「自身」解释。
// 早先它落到 CSS 引擎，cascadia 报 `CSS 选择器非法 "."`，整条抽取直接 0 条。
func TestDotSelectorMeansSelf(t *testing.T) {
	sel, err := Parse(".")
	if err != nil {
		t.Fatalf(`Parse(".") 失败: %v`, err)
	}
	if sel.Engine != "xpath" {
		t.Fatalf(`"." 应走 xpath，实际 %q`, sel.Engine)
	}
	spec := model.Extract{
		Item: "xpath://ul/li/a", // 条目根就是链接本身：字段只能写 "."（自身）
		Fields: map[string]model.Field{
			"title": {Sel: "."},
			"link":  {Sel: ".", Attr: "href"},
		},
	}
	res, err := HTML([]byte(listHTML), "https://example.com/news.aspx", spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("应产出 2 条，实际 %d（警告 %v）", len(res.Items), res.Warnings)
	}
	if got := res.Items[1].Link; got != "https://example.com/news-detail.aspx?id=2" {
		t.Fatalf("sel=. 取 href 失败：%q", got)
	}
}
