package bilibili

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadOpusDetailFixture 读取真实抓取的 opus 详情响应（已脱敏，只保留结构）。
func loadOpusDetailFixture(t *testing.T) *opusDetailItem {
	t.Helper()
	raw, err := os.ReadFile("testdata/opus_detail.json")
	require.NoError(t, err)
	var detail opusDetailResponse
	require.NoError(t, json.Unmarshal(raw, &detail))
	require.NotNil(t, detail.Data)
	require.NotNil(t, detail.Data.Item)
	return detail.Data.Item
}

// 详情接口的正文必须把内嵌图片换成 [图N] 标记，并把内嵌视频节点展开成“标题 链接”。
func TestOpusDetailRender(t *testing.T) {
	content := loadOpusDetailFixture(t).render()
	require.NotNil(t, content)

	// 抓包那条动态：正文 12 张图、3 个内嵌视频链接
	assert.Len(t, content.Pics, 12)
	assert.Contains(t, content.Text, "[图1]")
	assert.Contains(t, content.Text, "[图12]")
	assert.Contains(t, content.Text, "大肥鱼“报恩” https://b23.tv/OdUvXDg")
	assert.Contains(t, content.Text, "https://b23.tv/OmuCmlh")
	assert.Contains(t, content.Text, "https://b23.tv/Y0JLmma")

	// 原始正文里的占位符不应该再出现
	assert.NotContains(t, content.Text, "[图片]")
	assert.NotContains(t, content.Text, "\r\n")
}

// 段落文本要按原顺序拼接，标题与链接之间用空格分隔，链接不重复追加。
func TestRenderOpusTextParagraph(t *testing.T) {
	para := &opusParaText{Nodes: []*opusTextNode{
		{Type: "TEXT_NODE_TYPE_WORD", Word: &opusTextWord{Words: "看这个："}},
		{Type: "TEXT_NODE_TYPE_RICH", Rich: &opusTextRich{
			Type: "RICH_TEXT_NODE_TYPE_AV", Text: "某个视频", JumpURL: "https://b23.tv/abc",
		}},
		{Type: "TEXT_NODE_TYPE_RICH", Rich: &opusTextRich{
			Type: "RICH_TEXT_NODE_TYPE_WEB", Text: "", OrigText: "网页链接", JumpURL: "https://example.com/x",
		}},
		{Type: "TEXT_NODE_TYPE_RICH", Rich: &opusTextRich{
			Type: "RICH_TEXT_NODE_TYPE_WEB", Text: "https://example.com/y", JumpURL: "https://example.com/y",
		}},
		{Type: "TEXT_NODE_TYPE_RICH"},
	}}
	var b strings.Builder
	renderOpusTextParagraph(&b, para)
	assert.Equal(t, "看这个：某个视频 https://b23.tv/abc网页链接 https://example.com/xhttps://example.com/y", b.String())
}

// 图片段落按出现顺序编号，空 URL 的图片要跳过。
func TestRenderOpusPicParagraph(t *testing.T) {
	var b strings.Builder
	content := new(opusContent)
	renderOpusPicParagraph(&b, &opusParaPic{Pics: []*polymerPic{
		{URL: "https://i0.hdslb.com/a.jpg"},
		{URL: ""},
		{URL: "https://i0.hdslb.com/b.jpg"},
	}}, content)

	assert.Equal(t, "[图1]\n[图2]\n", b.String())
	require.Len(t, content.Pics, 2)
	assert.Equal(t, "https://i0.hdslb.com/a.jpg", content.Pics[0].URL)
	assert.Equal(t, "https://i0.hdslb.com/b.jpg", content.Pics[1].URL)
}

func TestNormalizeOpusText(t *testing.T) {
	in := "第一行   \n\n\n\n第二行\u3000\n\n"
	assert.Equal(t, "第一行\n\n第二行", normalizeOpusText(in))
}

// 补齐后的动态：正文来自详情接口，图片是正文里的 12 张，而不是动态流里的封面。
func TestEnrichOpusContent(t *testing.T) {
	fixture := loadOpusDetailFixture(t)
	resetOpusContentCache()
	stubOpusContentFetcher(t, func(idStr string) (*opusContent, error) {
		assert.Equal(t, "1265660629497151489", idStr)
		return fixture.render(), nil
	})

	item := &polymerItem{
		IDStr: "1265660629497151489",
		Modules: &polymerModules{ModuleDynamic: &polymerModuleDynamic{
			Desc: &polymerDesc{Text: "正文\n[图片]\n[图片]"},
			Major: &polymerMajor{
				Type: "MAJOR_TYPE_OPUS",
				Opus: &polymerOpus{
					Summary: &polymerDesc{Text: "正文\n[图片]\n[图片]"},
					Pics:    []*polymerPic{{URL: "https://i0.hdslb.com/cover.jpg"}},
				},
			},
		}},
	}

	item.enrichOpusContent()

	opus := item.major().Opus
	require.NotNil(t, opus)
	assert.Len(t, opus.Pics, 12)
	assert.Contains(t, opus.Summary.Text, "大肥鱼“报恩” https://b23.tv/OdUvXDg")
	assert.NotEqual(t, "https://i0.hdslb.com/cover.jpg", opus.Pics[0].URL)

	card, err := polymerItemToCard(item)
	require.NoError(t, err)
	assert.Equal(t, DynamicDescType_WithImage, card.GetDesc().GetType())
}

// 详情接口失败时必须原样保留动态流里的正文与图片，不能把动态丢掉。
func TestEnrichOpusContentFallback(t *testing.T) {
	resetOpusContentCache()
	stubOpusContentFetcher(t, func(string) (*opusContent, error) {
		return nil, assert.AnError
	})

	item := &polymerItem{
		IDStr: "1",
		Modules: &polymerModules{ModuleDynamic: &polymerModuleDynamic{
			Major: &polymerMajor{
				Type: "MAJOR_TYPE_OPUS",
				Opus: &polymerOpus{
					Summary: &polymerDesc{Text: "正文\n[图片]"},
					Pics:    []*polymerPic{{URL: "https://i0.hdslb.com/cover.jpg"}},
				},
			},
		}},
	}

	item.enrichOpusContent()

	opus := item.major().Opus
	require.NotNil(t, opus)
	assert.Equal(t, "正文\n[图片]", opus.Summary.Text)
	require.Len(t, opus.Pics, 1)
	assert.Equal(t, "https://i0.hdslb.com/cover.jpg", opus.Pics[0].URL)
}

// 转发动态的原动态也要补齐，但只补一层，避免脏数据里出现自引用时无限递归。
func TestEnrichOpusContentOriginDepth(t *testing.T) {
	resetOpusContentCache()
	calls := 0
	stubOpusContentFetcher(t, func(idStr string) (*opusContent, error) {
		calls++
		return &opusContent{Text: "详情 " + idStr}, nil
	})

	origin := &polymerItem{
		IDStr: "2",
		Modules: &polymerModules{ModuleDynamic: &polymerModuleDynamic{
			Major: &polymerMajor{Type: "MAJOR_TYPE_OPUS", Opus: &polymerOpus{Summary: &polymerDesc{Text: "原动态"}}},
		}},
	}
	origin.Orig = &polymerItem{
		IDStr: "3",
		Modules: &polymerModules{ModuleDynamic: &polymerModuleDynamic{
			Major: &polymerMajor{Type: "MAJOR_TYPE_OPUS", Opus: &polymerOpus{Summary: &polymerDesc{Text: "更深的原动态"}}},
		}},
	}
	item := &polymerItem{
		IDStr: "1",
		Modules: &polymerModules{ModuleDynamic: &polymerModuleDynamic{
			Major: &polymerMajor{Type: "MAJOR_TYPE_OPUS", Opus: &polymerOpus{Summary: &polymerDesc{Text: "正文"}}},
		}},
	}
	item.Orig = origin

	item.enrichOpusContent()

	assert.Equal(t, 2, calls)
	assert.Equal(t, "详情 1", item.major().Opus.Summary.Text)
	assert.Equal(t, "详情 2", origin.major().Opus.Summary.Text)
	assert.Equal(t, "更深的原动态", origin.Orig.major().Opus.Summary.Text)
}

// 同一轮渲染里正文和图片会各取一次，缓存必须只打一次接口。
func TestLoadOpusContentCache(t *testing.T) {
	resetOpusContentCache()
	calls := 0
	stubOpusContentFetcher(t, func(string) (*opusContent, error) {
		calls++
		return &opusContent{Text: "正文"}, nil
	})

	first := loadOpusContent("42")
	second := loadOpusContent("42")

	require.NotNil(t, first)
	assert.Same(t, first, second)
	assert.Equal(t, 1, calls)
}

func resetOpusContentCache() {
	opusContentCache.Lock()
	opusContentCache.entries = make(map[string]opusCacheEntry)
	opusContentCache.Unlock()
}

func stubOpusContentFetcher(t *testing.T, fn func(string) (*opusContent, error)) {
	t.Helper()
	original := opusContentFetcher
	opusContentFetcher = fn
	t.Cleanup(func() {
		opusContentFetcher = original
		resetOpusContentCache()
	})
}
