package template

import (
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

func TestReplaceAvifWithPng(t *testing.T) {
	// 期望值按成品行为（funcs_request_ext.go:959 @0xb52a80）：补 "//" 协议、
	// 把第一个 http:// 换成 https://、仅当后缀是 .avif/.webp 时替换为 .png，
	// 不给其它后缀追加 .png。
	tests := []struct {
		input    string
		expected string
	}{
		{
			"//i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
		},
		{
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
		},
		{
			"//i0.hdslb.com/bfs/new_dyn/abc.avif",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png",
		},
		{
			"http://i0.hdslb.com/bfs/new_dyn/abc.webp",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png",
		},
		{
			"//i0.hdslb.com/bfs/new_dyn/abc.jpg",
			"https://i0.hdslb.com/bfs/new_dyn/abc.jpg",
		},
	}
	for _, tt := range tests {
		got := replaceAvifWithPng(tt.input)
		if got != tt.expected {
			t.Errorf("replaceAvifWithPng(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseBiliPostContent_MissingElements(t *testing.T) {
	html := `<html><head><script>
		window.__INITIAL_STATE__={"module_collection":{"count":"1篇"},"collection_id":12345};
		</script></head><body><div class="opus-module-content">
		<div class="opus-para-link-card">
			<a href="https://www.bilibili.com/opus/1">
				<span class="opus-tag tag-dynamic">动态</span>
				<span class="opus-title">关联动态</span>
				<div class="opus-cover"><img src="//i0.hdslb.com/bfs/new_dyn/link.png@128w_128h_1c"/></div>
			</a>
		</div>
		</div></body></html>`
	elements := parseBiliPostContent([]byte(html))

	var types []string
	for _, e := range elements {
		types = append(types, e.Type)
	}

	t.Logf("Total elements: %d", len(elements))
	for i, e := range elements {
		t.Logf("[%d] type=%q ele=%q desc=%q url=%q", i, e.Type, e.Ele, e.Desc, e.Url)
	}

	// Verify collection is present
	hasCollection := false
	for _, e := range elements {
		if e.Type == "collection" {
			hasCollection = true
			if e.Ele != "1" {
				t.Errorf("collection count = %q, want 1", e.Ele)
			}
			if !strings.Contains(e.Url, "rl") {
				t.Errorf("collection url = %q, want containing 'rl'", e.Url)
			}
		}
	}

	// Verify link-card has Image field
	hasLinkCardImage := false
	for _, e := range elements {
		if e.Type == "dynamic-card" && e.Image != "" {
			hasLinkCardImage = true
			// 成品 replaceAvifWithPng 只把 "//" 补成 https://，不给非 .avif/.webp
			// 的地址追加 .png（funcs_request_ext.go:959 @0xb52a80）。
			if !strings.HasPrefix(e.Image, "https://") || !strings.Contains(e.Image, "link.png") {
				t.Errorf("link-card image = %q, want https://...link.png...", e.Image)
			}
			t.Logf("dynamic-card image: %s", e.Image)
		}
	}
	if !hasLinkCardImage {
		t.Error("missing link-card image")
	}
	if !hasCollection {
		t.Error("missing collection element")
	}

	t.Logf("Seen types: %v", types)
}

func TestParseBiliPostContent_ImageCaption(t *testing.T) {
	html := `<html><body><div class="opus-module-content">
		<div class="opus-para-pic center">
			<div class="opus-pic-view">
				<div class="bili-dyn-pic">
					<img src="//i0.hdslb.com/bfs/new_dyn/test.jpg"/>
				</div>
				<div class="opus-pic-view__caption">这是鱼</div>
			</div>
		</div>
		</div></body></html>`

	elements := parseBiliPostContent([]byte(html))

	if len(elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(elements))
	}
	if elements[0].Type != "image" {
		t.Errorf("expected type=image, got %s", elements[0].Type)
	}
	if elements[0].Desc != "这是鱼" {
		t.Errorf("expected desc=这是鱼, got %s", elements[0].Desc)
	}
}

// TestBiliJSONParagraphParsing 用合成页面验证 opus JSON 段落/链接卡片的解析：
// biliParagraphsJSON 的 JSON 截取、biliTextAlignMap 的对齐映射，
// 以及 biliLinkCardMetadataFromJSON 的字段与 json tag。
func TestBiliJSONParagraphParsing(t *testing.T) {
	page := `<html><head><script>window.__INITIAL_STATE__={};
"module_content":{"paragraphs":[
  {"para_type":1,"align":1,"format":{"align":2},"text":{"nodes":[{"word":{"words":"hello"}}]}},
  {"para_type":4,"blockquote":{"children":[{"text":{"nodes":[{"word":{"words":"child"}}]}}]}},
  {"link_card":{"card":{"type":"opus","eva3_opus":{"info":{
      "opus_id":"123","title":"T","jump_url":"https://www.bilibili.com/opus/123",
      "author_name":"A","view":"1.2万","reply":"3","badge":"b",
      "pics":[{"src":"//i0.hdslb.com/x.avif"}]}}}}}
]}`
	raw := []byte(page)

	paras := biliParagraphsJSON(raw)
	if len(paras) == 0 || paras[0] != '[' || paras[len(paras)-1] != ']' {
		t.Fatalf("biliParagraphsJSON = %q", string(paras))
	}

	aligns := biliTextAlignMap(raw)
	if aligns["hello"] != "right" {
		t.Errorf("align[hello] = %q, want right", aligns["hello"])
	}
	if v, ok := aligns["child"]; !ok || v != "" {
		t.Errorf("align[child] = %q,%v want empty,true", v, ok)
	}

	metas := biliLinkCardMetadataFromJSON(raw)
	if len(metas) != 1 {
		t.Fatalf("metas = %d, want 1", len(metas))
	}
	m := metas[0]
	if m.Type != "dynamic-card" || m.Identity != "opus:123" {
		t.Errorf("type=%q identity=%q", m.Type, m.Identity)
	}
	if m.URL != "https://www.bilibili.com/opus/123" || m.Title != "T" || m.Author != "A" {
		t.Errorf("url=%q title=%q author=%q", m.URL, m.Title, m.Author)
	}
	if m.Views != "1.2万" || m.Danmaku != "3" || m.CardTag != "b" {
		t.Errorf("views=%q danmaku=%q cardtag=%q", m.Views, m.Danmaku, m.CardTag)
	}
	if m.Image != "https://i0.hdslb.com/x.png" {
		t.Errorf("image=%q", m.Image)
	}
	if m.Duration != "" {
		t.Errorf("duration=%q, want empty for single pic", m.Duration)
	}
}

func TestParseBiliPostContent_UserURL(t *testing.T) {
	opts := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
	}
	var body strings.Builder
	err := requests.Get("https://www.bilibili.com/opus/1196749689520652312", nil, &body, opts...)
	if err != nil {
		t.Skipf("skipping test: failed to fetch page: %v", err)
	}

	elements := parseBiliPostContent([]byte(body.String()))

	t.Logf("Total elements: %d", len(elements))
	for i, e := range elements {
		t.Logf("[%d] type=%q ele=%q desc=%q url=%q image=%q", i, e.Type, e.Ele, e.Desc, e.Url, e.Image)
	}
}
