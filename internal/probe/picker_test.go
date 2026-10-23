package probe

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// 预览是去掉脚本后的静态 DOM，站点自己的 loading 层不会关闭；拾取器必须兜底隐藏。
func TestPickerHidesFrozenLoadingOverlays(t *testing.T) {
	js := string(PickerJS())
	for _, want := range []string{
		".at-activity-indicator",
		".taro__toast",
		"加载中",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("picker.js 缺少加载层清理片段 %q", want)
		}
	}
}

// 结构与 suda.meiyuedu.cn/web/ArtIndex 一致：条目在一个带 id 的祖先下面
// （<div class="artshow"><div id="infoArea"><div class="leftArea"><a><div class="list_item">…）。
const idAncestorPage = `<html><head><title>美育实践</title></head><body>
<div class="artshow">
  <div id="infoArea">
    <div class="leftArea area_item">
      <a href="/web/ArtDetail?ArtId=1326"><div class="list_item">
        <div class="list_top"><img src="/x.png"><div class="rtIcon rtIcon_success">报名中</div></div>
        <div class="list_bottom"><h3>扬剧《百岁挂帅》</h3><p><span>主办单位：</span><span>苏州大学</span></p></div>
      </div></a>
      <a href="/web/ArtDetail?ArtId=1325"><div class="list_item">
        <div class="list_bottom"><h3>读写之美</h3></div>
      </div></a>
    </div>
  </div>
</div></body></html>`

// pickerXPathOf 逐行照抄 picker.js 的 xpathOf。改任一侧都要同步改这里，
// 否则「浏览器算路径、后端重放」这条链路会悄悄断掉。
func pickerXPathOf(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	if id := attr(n, "id"); id != "" {
		return "//*[@id=" + quoteXPath(id) + "]"
	}
	var parts []string
	for cur := n; cur != nil && cur.Type == html.ElementNode; {
		if id := attr(cur, "id"); id != "" {
			parts = append([]string{"*[@id=" + quoteXPath(id) + "]"}, parts...)
			return "//" + strings.Join(parts, "/")
		}
		i := 1
		for sib := cur.PrevSibling; sib != nil; sib = sib.PrevSibling {
			if sib.Type == html.ElementNode && sib.Data == cur.Data {
				i++
			}
		}
		seg := cur.Data
		if cls := firstClass(cur); cls != "" {
			seg += "[contains(@class," + quoteXPath(cls) + ")]"
		} else {
			seg += fmt.Sprintf("[%d]", i)
		}
		parts = append([]string{seg}, parts...)
		cur = cur.Parent
	}
	p := strings.Join(parts, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func firstOfTag(t *testing.T, root *html.Node, tag string) *html.Node {
	t.Helper()
	var walk func(*html.Node) *html.Node
	walk = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.Data == tag {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if got := walk(c); got != nil {
				return got
			}
		}
		return nil
	}
	got := walk(root)
	if got == nil {
		t.Fatalf("夹具里没有 <%s>", tag)
	}
	return got
}

func htmlSession(t *testing.T, page, url string) (*Probe, string, *html.Node) {
	t.Helper()
	safe, err := sanitize([]byte(page), url)
	if err != nil {
		t.Fatalf("sanitize 失败: %v", err)
	}
	p := &Probe{sessions: map[string]*Session{}}
	p.sessions["h"] = &Session{
		ID: "h", URL: url, HTML: safe, Raw: []byte(page), Kind: "html", FetchedAt: time.Now(),
	}
	doc, err := html.Parse(bytes.NewReader(safe))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return p, "h", doc
}

// 祖先带 id 时，路径必须以 //*[@id=…] 开头：/… 只会去匹配 <html>，
// 而 id 这一步要取代该祖先自身的片段（两个都留就会指到它的子节点上）。
func TestPickerXPathStartsAtIDAncestor(t *testing.T) {
	_, _, doc := htmlSession(t, idAncestorPage, "https://suda.meiyuedu.cn/web/ArtIndex")
	got := pickerXPathOf(firstOfTag(t, doc, "h3"))
	want := "//*[@id='infoArea']/div[contains(@class,'leftArea')]/a[1]/div[contains(@class,'list_item')]/div[contains(@class,'list_bottom')]/h3[1]"
	if got != want {
		t.Fatalf("xpath 不符\n got: %s\nwant: %s", got, want)
	}
	if strings.HasPrefix(got, "/*[@id=") {
		t.Errorf("绝对路径用单斜杠开头，后端必然 0 命中: %s", got)
	}
}

// 浏览器算出的路径必须在后端重放命中，否则点选就是一句「选择器没有命中节点」。
func TestSuggestReplaysPickerXPath(t *testing.T) {
	p, sid, doc := htmlSession(t, idAncestorPage, "https://suda.meiyuedu.cn/web/ArtIndex")
	title := firstOfTag(t, doc, "h3")

	// 拾取器给的路径与后端 absXPath 必须是同一条
	if got, want := pickerXPathOf(title), absXPath(title); got != want {
		t.Errorf("拾取器与后端路径不一致\n拾取器: %s\n后端:   %s", got, want)
	}
	sug, err := p.Suggest(sid, pickerXPathOf(title), "")
	if err != nil {
		t.Fatalf("Suggest 失败: %v", err)
	}
	if sug.Tag != "h3" {
		t.Errorf("命中的是 <%s>，不是 <h3>", sug.Tag)
	}
	var items int
	for _, c := range sug.Candidates {
		if c.Kind == "item" {
			items++
		}
	}
	if items == 0 {
		t.Errorf("没有给出条目候选: %+v", sug.Candidates)
	}
	// 字段推断以条目为根时，标题与链接都要能取到
	sug, err = p.Suggest(sid, pickerXPathOf(title), "//*[@id='infoArea']/div[contains(@class,'leftArea')]//a")
	if err != nil {
		t.Fatalf("带条目选择器时 Suggest 失败: %v", err)
	}
	var hasTitle, hasLink bool
	for _, f := range sug.Fields {
		hasTitle = hasTitle || f.Name == "title"
		hasLink = hasLink || f.Name == "link"
	}
	if !hasTitle || !hasLink {
		t.Errorf("字段推断缺项: %+v", sug.Fields)
	}
}

// 表格里浏览器会补 tbody，后端这一侧必须同步，否则整条路径错位。
func TestSuggestReplaysPickerXPathInsideTable(t *testing.T) {
	page := `<html><body><table><tr><td><a href="/a">标题A</a></td></tr>` +
		`<tr><td><a href="/b">标题B</a></td></tr></table></body></html>`
	p, sid, doc := htmlSession(t, page, "https://example.com/")
	xp := pickerXPathOf(firstOfTag(t, doc, "a"))
	if n, _, err := p.Count(sid, "xpath:"+xp); err != nil || n != 1 {
		t.Fatalf("表格内路径 %s 命中 %d，err=%v", xp, n, err)
	}
}

// 拾取器脚本里的 id 分支：先判 id 再拼片段，且返回 // 开头。
func TestPickerXPathIDBranch(t *testing.T) {
	js := string(PickerJS())
	for _, want := range []string{
		"if (el.id) return '//*[@id=' + q(el.id) + ']'",
		"return '//' + parts.join('/')",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("picker.js 缺少 id 分支片段 %q", want)
		}
	}
	if strings.Contains(js, "parts.unshift(\"*[@id='\" + cur.id + \"']\")") {
		t.Error("picker.js 还在把 id 步骤与节点自身片段叠在一起（会多指一层）")
	}
}
