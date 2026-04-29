package template

import (
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

func TestReplaceAvifWithPng(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"//i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c.png",
		},
		{
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c",
			"https://i0.hdslb.com/bfs/new_dyn/abc.png@128w_128h_1c.png",
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
	opts := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
	}
	var body strings.Builder
	err := requests.Get("https://www.bilibili.com/opus/1180763965117956099", nil, &body, opts...)
	if err != nil {
		t.Skipf("skipping test: failed to fetch page: %v", err)
	}

	elements := parseBiliPostContent([]byte(body.String()))

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
			if !strings.HasSuffix(e.Image, ".png") {
				t.Errorf("link-card image = %q, want ending with .png", e.Image)
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
