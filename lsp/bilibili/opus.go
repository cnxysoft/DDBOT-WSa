package bilibili

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// PathPolymerOpusDetail 是图文（opus）动态的详情接口。
//
// 动态流接口里的 opus 只带 summary.text 和少量封面图：正文中的内嵌图片被压成
// 字面占位符“[图片]”，内嵌视频/专栏等富文本节点只剩标题、没有链接。
// 真正按段落组织的正文（含图片与 jump_url）只在这个接口返回的
// modules[MODULE_TYPE_CONTENT].paragraphs 里。
const PathPolymerOpusDetail = "/x/polymer/web-dynamic/v1/opus/detail"

// opusContentTTL 是 opus 详情的内存缓存时长。一条动态在渲染过程中会被
// 反复取正文与图片，缓存避免重复请求；同时它也覆盖了“同一轮刷新里
// 正文与图片各取一次”的场景。
const opusContentTTL = 10 * time.Minute

// opusContentCacheLimit 限制缓存条目，避免长跑进程无限增长。
const opusContentCacheLimit = 256

// opusContent 是从详情接口还原出的正文。
type opusContent struct {
	// Text 是渲染后的正文：富文本节点展开为“标题 链接”，
	// 内嵌图片按出现顺序写成 [图1]、[图2]……
	Text string
	// Pics 是正文内嵌图片，顺序与 Text 中的 [图N] 标记一致。
	Pics []*polymerPic
}

type opusDetailResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    *opusDetailDoc `json:"data"`
}

type opusDetailDoc struct {
	Item *opusDetailItem `json:"item"`
}

type opusDetailItem struct {
	IDStr   string        `json:"id_str"`
	Modules []*opusModule `json:"modules"`
}

type opusModule struct {
	ModuleType    string             `json:"module_type"`
	ModuleContent *opusModuleContent `json:"module_content"`
}

type opusModuleContent struct {
	Paragraphs []*opusParagraph `json:"paragraphs"`
}

type opusParagraph struct {
	ParaType int           `json:"para_type"`
	Text     *opusParaText `json:"text"`
	Pic      *opusParaPic  `json:"pic"`
}

type opusParaText struct {
	Nodes []*opusTextNode `json:"nodes"`
}

type opusTextNode struct {
	Type string        `json:"type"`
	Word *opusTextWord `json:"word"`
	Rich *opusTextRich `json:"rich"`
}

type opusTextWord struct {
	Words string `json:"words"`
}

type opusTextRich struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	OrigText string `json:"orig_text"`
	JumpURL  string `json:"jump_url"`
}

type opusParaPic struct {
	Pics []*polymerPic `json:"pics"`
}

type opusCacheEntry struct {
	content  *opusContent
	expireAt time.Time
}

var opusContentCache = struct {
	sync.Mutex
	entries map[string]opusCacheEntry
}{entries: make(map[string]opusCacheEntry)}

// opusContentFetcher 是详情接口的可替换入口，测试里用它注入固定数据。
var opusContentFetcher = requestOpusContent

// enrichOpusContent 用详情接口的正文与图片覆盖动态流里的残缺版本。
//
// 只对 opus（图文）主体生效；请求失败时保持原样，让下游退回旧行为，
// 不会因为一次风控或网络抖动就把整条动态丢掉。
func (i *polymerItem) enrichOpusContent() {
	i.enrichOpusContentDepth(0)
}

func (i *polymerItem) enrichOpusContentDepth(depth int) {
	if i == nil || depth > 1 {
		return
	}
	if opus := i.major().Opus; opus != nil {
		if content := loadOpusContent(i.IDStr); content != nil {
			if content.Text != "" {
				if opus.Summary == nil {
					opus.Summary = new(polymerDesc)
				}
				opus.Summary.Text = content.Text
			}
			if len(content.Pics) > 0 {
				opus.Pics = content.Pics
			}
		}
	}
	if i.Orig != nil {
		i.Orig.enrichOpusContentDepth(depth + 1)
	}
}

// loadOpusContent 读取（或拉取）指定动态的 opus 正文。
func loadOpusContent(idStr string) *opusContent {
	if idStr == "" {
		return nil
	}
	now := time.Now()
	opusContentCache.Lock()
	if entry, ok := opusContentCache.entries[idStr]; ok && now.Before(entry.expireAt) {
		opusContentCache.Unlock()
		return entry.content
	}
	opusContentCache.Unlock()

	content, err := opusContentFetcher(idStr)
	if err != nil {
		logger.WithError(err).WithField("id_str", idStr).Debugf("获取 opus 详情失败，按动态流里的正文推送")
		return nil
	}

	opusContentCache.Lock()
	if len(opusContentCache.entries) >= opusContentCacheLimit {
		opusContentCache.entries = make(map[string]opusCacheEntry)
	}
	opusContentCache.entries[idStr] = opusCacheEntry{content: content, expireAt: now.Add(opusContentTTL)}
	opusContentCache.Unlock()
	return content
}

func requestOpusContent(idStr string) (*opusContent, error) {
	url := BPath(PathPolymerOpusDetail)
	params := map[string]string{"id": idStr}
	var resp bytes.Buffer
	opts := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(2),
	}
	opts = append(opts, GetVerifyOption()...)
	if err := requests.Get(url, params, &resp, opts...); err != nil {
		return nil, err
	}
	var detail opusDetailResponse
	if err := json.Unmarshal(resp.Bytes(), &detail); err != nil {
		return nil, err
	}
	if detail.Code != 0 {
		return nil, fmt.Errorf("opus detail code=%d message=%s", detail.Code, detail.Message)
	}
	if detail.Data == nil || detail.Data.Item == nil {
		return nil, errors.New("opus detail 缺少 item")
	}
	return detail.Data.Item.render(), nil
}

// render 把按段落组织的正文还原成可读文本与图片列表。
func (it *opusDetailItem) render() *opusContent {
	content := new(opusContent)
	if it == nil {
		return content
	}
	var b strings.Builder
	for _, module := range it.Modules {
		if module == nil || module.ModuleContent == nil {
			continue
		}
		if module.ModuleType != "MODULE_TYPE_CONTENT" && module.ModuleType != "" {
			continue
		}
		for _, para := range module.ModuleContent.Paragraphs {
			if para == nil {
				continue
			}
			switch para.ParaType {
			case 1:
				renderOpusTextParagraph(&b, para.Text)
				b.WriteString("\n")
			case 2:
				renderOpusPicParagraph(&b, para.Pic, content)
			}
		}
	}
	content.Text = normalizeOpusText(b.String())
	return content
}

func renderOpusTextParagraph(b *strings.Builder, para *opusParaText) {
	if para == nil {
		return
	}
	for _, node := range para.Nodes {
		if node == nil {
			continue
		}
		switch node.Type {
		case "TEXT_NODE_TYPE_WORD":
			if node.Word != nil {
				b.WriteString(node.Word.Words)
			}
		default:
			if node.Rich == nil {
				continue
			}
			text := node.Rich.Text
			if text == "" {
				text = node.Rich.OrigText
			}
			b.WriteString(text)
			if node.Rich.JumpURL == "" || strings.Contains(text, node.Rich.JumpURL) {
				continue
			}
			if strings.TrimSpace(text) != "" {
				b.WriteString(" ")
			}
			b.WriteString(node.Rich.JumpURL)
		}
	}
}

func renderOpusPicParagraph(b *strings.Builder, para *opusParaPic, content *opusContent) {
	if para == nil {
		return
	}
	for _, pic := range para.Pics {
		if pic == nil || pic.URL == "" {
			continue
		}
		content.Pics = append(content.Pics, pic)
		fmt.Fprintf(b, "[图%d]\n", len(content.Pics))
	}
}

// normalizeOpusText 收敛空行与行尾空白，避免详情段落合并出一大段空白。
func normalizeOpusText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\u3000")
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
