package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/model"
)

const sampleJSON = `{"code":0,"count":2,"data":[
{"ArtId":1326,"ArtName":"扬剧《百岁挂帅》","ArtPic":"/x.png","StartTime":"2026-10-18 18:30:00","ArtContent":"<p>正文</p>"},
{"ArtId":1325,"ArtName":"读写之美","ArtPic":"/y.png","StartTime":"2026-09-22 14:00:00","ArtContent":"<p>正文2</p>"}]}`

func jsonDoc(t *testing.T) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(sampleJSON), &doc); err != nil {
		t.Fatalf("样例 JSON 解析失败: %v", err)
	}
	return doc
}

func jsonSession(t *testing.T) (*Probe, string) {
	t.Helper()
	p := &Probe{sessions: map[string]*Session{}}
	p.sessions["js"] = &Session{
		ID: "js", URL: "https://example.com/api", Kind: "json",
		Doc: jsonDoc(t), Raw: []byte(sampleJSON),
	}
	return p, "js"
}

func TestJSONTreeRendersClickablePaths(t *testing.T) {
	out := string(jsonTreeHTML(jsonDoc(t), ""))
	for _, want := range []string{
		`data-mode="json"`,
		`data-jpath="$.data"`,
		`data-jpath="$.data[0].ArtName"`,
		`data-jpath="$.data[1].StartTime"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("预览树缺少 %s", want)
		}
	}
	// 值必须转义，接口返回什么都不能变成标签
	if strings.Contains(out, "<p>正文</p>") {
		t.Fatalf("值未转义，可注入 HTML: %.200s", out)
	}
}

func TestJSONTreeFoldsDeepLevelsAndHidesTail(t *testing.T) {
	big := map[string]any{"list": []any{}}
	list := make([]any, 12)
	for i := range list {
		list[i] = map[string]any{"title": "t", "body": map[string]any{"deep": "x"}}
	}
	big["list"] = list
	out := string(jsonTreeHTML(big, ""))
	if !strings.Contains(out, "还有 4 条") {
		t.Fatalf("数组尾部应折叠成「还有 N 条」: %.300s", out)
	}
	if !strings.Contains(out, `class="c rest"`) {
		t.Fatalf("剩余记录应放在 .rest 容器里: %.300s", out)
	}
	// 第 3 层（body）默认折叠
	if !strings.Contains(out, `class="r kid fold"`) {
		t.Fatalf("深层节点应默认折叠: %.300s", out)
	}
}

func TestJSONSuggestGivesItemPathAndFields(t *testing.T) {
	p, sid := jsonSession(t)
	sug, err := p.Suggest(sid, "$.data[0].ArtName", "")
	if err != nil {
		t.Fatalf("建议失败: %v", err)
	}
	var item *Candidate
	for i := range sug.Candidates {
		if sug.Candidates[i].Kind == "item" {
			item = &sug.Candidates[i]
		}
	}
	if item == nil {
		t.Fatalf("点字段时应给出所属数组的条目候选，实际 %+v", sug.Candidates)
	}
	if item.XPath != "json:$.data" || item.Count != 2 {
		t.Fatalf("条目候选不对: %+v", *item)
	}
	want := map[string]string{"title": "$.ArtName", "date": "$.StartTime", "guid": "$.ArtId"}
	got := map[string]string{}
	for _, f := range sug.Fields {
		got[f.Name] = f.Sel
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("字段 %s 推断为 %q，期望 %q（全部: %+v）", k, got[k], v, got)
		}
	}
}

func TestJSONSuggestOnArrayItself(t *testing.T) {
	p, sid := jsonSession(t)
	sug, err := p.Suggest(sid, "$.data", "")
	if err != nil {
		t.Fatalf("建议失败: %v", err)
	}
	if len(sug.Candidates) == 0 || sug.Candidates[0].XPath != "json:$.data" {
		t.Fatalf("点数组时应直接给出数组路径: %+v", sug.Candidates)
	}
	if len(sug.Fields) == 0 {
		t.Fatal("点数组时也要按首条记录推断字段")
	}
}

func TestJSONCountAndExtract(t *testing.T) {
	p, sid := jsonSession(t)
	n, samples, err := p.Count(sid, "json:$.data")
	if err != nil || n != 2 {
		t.Fatalf("计数不对: n=%d err=%v", n, err)
	}
	if len(samples) == 0 || !strings.Contains(samples[0], "扬剧") {
		t.Fatalf("样例应取标题类字段: %+v", samples)
	}
	res, err := p.Extract(sid, model.Extract{
		Item:   "json:$.data",
		Fields: map[string]model.Field{"title": {Sel: "$.ArtName"}, "guid": {Sel: "$.ArtId"}},
	}, nil)
	if err != nil {
		t.Fatalf("抽取失败: %v", err)
	}
	if len(res.Items) != 2 || res.Items[0].Title != "扬剧《百岁挂帅》" {
		t.Fatalf("抽取结果不对: %+v", res.Items)
	}
}

func TestJSONExtractAbsolutizesRelativeLink(t *testing.T) {
	p, sid := jsonSession(t)
	res, err := p.Extract(sid, model.Extract{
		Item: "$.data",
		Fields: map[string]model.Field{
			"title": {Sel: "$.ArtName"},
			"link":  {Template: "/web/ArtDetail?ArtId={$.ArtId}"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("抽取失败: %v", err)
	}
	want := "https://example.com/web/ArtDetail?ArtId=1326"
	if len(res.Items) == 0 || res.Items[0].Link != want {
		t.Fatalf("相对链接未补全: %+v", res.Items)
	}
}

func TestProbeFetchJSONSessionFollowsAPI(t *testing.T) {
	var hitAPI, hitPage bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/list":
			hitAPI = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(sampleJSON))
		default:
			hitPage = true
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><head><title>壳</title></head><body></body></html>"))
		}
	}))
	defer srv.Close()

	f := fetch.New(fetch.Options{AllowPrivate: true})
	p := &Probe{fetcher: f, sessions: map[string]*Session{}}
	src := model.Source{
		URL:    srv.URL + "/index",
		Kind:   model.KindJSON,
		Engine: model.EngineHTTP,
		Request: model.Request{
			TimeoutSec: 10,
			API:        &model.APISpec{URL: srv.URL + "/api/list", Method: "GET"},
		},
	}
	sess, err := p.Fetch(t.Context(), src)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if !hitAPI || hitPage {
		t.Fatalf("接口型预览必须抓 api.url：api=%v page=%v", hitAPI, hitPage)
	}
	if sess.Kind != "json" {
		t.Fatalf("会话形态应为 json，实际 %q", sess.Kind)
	}
	if !strings.Contains(string(sess.HTML), `data-jpath="$.data"`) {
		t.Fatalf("预览应是可点选的 JSON 树: %.200s", sess.HTML)
	}
	if !strings.Contains(string(sess.HTML), "__siwuPickerReady") {
		t.Fatal("预览未注入拾取器")
	}
	if !strings.Contains(string(sess.Raw), `"ArtName"`) {
		t.Fatalf("Raw 必须是原始 JSON，供抽取复用: %.200s", sess.Raw)
	}
}

func TestProbeFetchDetectsJSONUnderHTMLKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleJSON))
	}))
	defer srv.Close()

	f := fetch.New(fetch.Options{AllowPrivate: true})
	p := &Probe{fetcher: f, sessions: map[string]*Session{}}
	sess, err := p.Fetch(t.Context(), model.Source{
		URL: srv.URL, Kind: model.KindHTML, Engine: model.EngineHTTP,
		Request: model.Request{TimeoutSec: 10},
	})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if sess.Kind != "json" {
		t.Fatalf("响应是 JSON 应按 JSON 预览，实际 %q", sess.Kind)
	}
	if len(sess.Warnings) == 0 || !strings.Contains(sess.Warnings[0], "接口型") {
		t.Fatalf("列表型抓到 JSON 应提示改类型，实际 %+v", sess.Warnings)
	}
}

func TestShellPageHint(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "静态列表页不提示",
			body: `<html><body><ul><li><a href="/a">第一条</a></li><li><a href="/b">第二条</a></li></ul>
<script>var a=1;</script></body></html>`,
			want: false,
		},
		{
			name: "脚本取数且无列表结构要提示并给出接口",
			body: `<html><body><div class="app"></div><footer>底部导航</footer>
<script>$.get('/Art/GetArtList1', function (res) { render(res.data) })</script></body></html>`,
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shellPageHint([]byte(c.body)) != ""
			if got != c.want {
				t.Fatalf("提示判定不对：want=%v got=%v", c.want, got)
			}
		})
	}
	hint := shellPageHint([]byte(cases[1].body))
	if !strings.Contains(hint, "/Art/GetArtList1") {
		t.Fatalf("提示里应带上脚本里的接口地址: %q", hint)
	}
}

func TestFirstAPIGuessSkipsAssets(t *testing.T) {
	script := `$.get('/Content/app.js'); $.post('/api/list');`
	if got := firstAPIGuess(script); got != "/api/list" {
		t.Fatalf("应跳过静态资源取到接口，实际 %q", got)
	}
}
