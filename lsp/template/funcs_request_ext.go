package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/google/uuid"
	"github.com/spf13/cast"
)

var (
	biliCollectionCountRe = regexp.MustCompile(`"module_collection"\s*:\s*\{[^}]*"count"\s*:\s*"(\d+)篇"`)
	biliCollectionIdRe    = regexp.MustCompile(`"collection_id"\s*:\s*(\d+)`)

	// 以下三个正则在成品 lsp/template.init（@0xb21700）里由 regexp.MustCompile 建立，
	// 分别写入 0x1bcc728 / 0x1bcc730 / 0x1bcc738 三个包级变量。
	biliColorRe    = regexp.MustCompile(`color:\s*([^;]+)`)
	biliFontSizeRe = regexp.MustCompile(`font-size:\s*([0-9.]+)px`)
	biliRgbaVarRe  = regexp.MustCompile(`rgba\(var\(--([A-Za-z0-9]+)_rgb\)\s*,\s*([0-9.]+)\)`)
)

// biliPostColorVars 是 B站 opus 正文里 var(--Xxx) 形式的调色板变量表。
//
// 证据：成品 map.init.0（funcs_request_ext.go:29，@0xb21ca0）用 10 次
// runtime.mapassign_faststr 往本表写入，键 3/4 字节、值均为 7 字节：
//
//	Re3  #fcafaa   Re7  #c9272c   Gr3  #95e4af   Ye3  #ffdb93   Pu7  #723ECC
//	Ly5  #ffcc00   Ga9  #2F3238   Ga10 #D0D3D7   Bk3  #18191C   Bg1  #FFFFFF
var biliPostColorVars = map[string]string{
	"Re3":  "#fcafaa",
	"Re7":  "#c9272c",
	"Gr3":  "#95e4af",
	"Ye3":  "#ffdb93",
	"Pu7":  "#723ECC",
	"Ly5":  "#ffcc00",
	"Ga9":  "#2F3238",
	"Ga10": "#D0D3D7",
	"Bk3":  "#18191C",
	"Bg1":  "#FFFFFF",
}

const (
	DDBOT_REQ_DEBUG      = "DDBOT_REQ_DEBUG"
	DDBOT_REQ_HEADER     = "DDBOT_REQ_HEADER"
	DDBOT_REQ_COOKIE     = "DDBOT_REQ_COOKIE"
	DDBOT_REQ_FETCH      = "DDBOT_REQ_FETCH"
	DDBOT_REQ_PROXY      = "DDBOT_REQ_PROXY"
	DDBOT_REQ_USER_AGENT = "DDBOT_REQ_USER_AGENT"
	DDBOT_REQ_TIMEOUT    = "DDBOT_REQ_TIMEOUT"
	DDBOT_REQ_RETRY      = "DDBOT_REQ_RETRY"
)

// PostRun 是 opus 正文的一段带样式文本。字段与内存布局由成品 reflect
// 元数据恢复：struct{ Text string; Color string; Size int; Bold bool }，共 48 字节。
type PostRun struct {
	Text  string
	Color string
	Size  int
	Bold  bool
}

// 以下类型对应 opus 正文 JSON（页面里 "module_content" 之后的 "paragraphs" 数组）。
// 结构体名、字段顺序与 json tag 均来自成品 reflect 元数据（recovered/types）。
type biliJSONFormat struct {
	Align int `json:"align"`
}

type biliJSONWord struct {
	Word *struct {
		Words string `json:"words"`
	} `json:"word"`
}

type biliJSONText struct {
	Nodes []biliJSONWord `json:"nodes"`
}

type biliJSONEva3Opus struct {
	Info *biliJSONOpusInfo `json:"info"`
}

type biliJSONEva3Video struct {
	Info *biliJSONVideoInfo `json:"info"`
}

type biliJSONLinkCard struct {
	Card *biliJSONLinkCardData `json:"card"`
}

type biliJSONLinkCardData struct {
	Type      string             `json:"type"`
	OID       string             `json:"oid"`
	Eva3Opus  *biliJSONEva3Opus  `json:"eva3_opus"`
	Eva3Video *biliJSONEva3Video `json:"eva3_video"`
}

type biliJSONVideoInfo struct {
	BVID       string `json:"bvid"`
	Cover      string `json:"cover"`
	JumpURL    string `json:"jump_url"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	StatInfos  []struct {
		Text string `json:"text"`
		Type int    `json:"type"`
	} `json:"stat_infos"`
	VideoTS *struct {
		DurationText string `json:"duration_text"`
	} `json:"video_ts"`
}

type biliJSONParagraph struct {
	ParaType   int             `json:"para_type"`
	Align      int             `json:"align"`
	Format     *biliJSONFormat `json:"format"`
	Text       *biliJSONText   `json:"text"`
	Heading    *biliJSONText   `json:"heading"`
	Blockquote *struct {
		Children []biliJSONParagraph `json:"children"`
	} `json:"blockquote"`
	LinkCard *biliJSONLinkCard `json:"link_card"`
}

type biliJSONOpusInfo struct {
	OpusID     string `json:"opus_id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	JumpURL    string `json:"jump_url"`
	AuthorName string `json:"author_name"`
	View       string `json:"view"`
	Reply      string `json:"reply"`
	Badge      string `json:"badge"`
	Pics       []struct {
		Src string `json:"src"`
	} `json:"pics"`
	CardBadge *struct {
		IconBadge *struct {
			Text string `json:"text"`
		} `json:"icon_badge"`
	} `json:"card_badge"`
}

// biliLinkCardMetadata 是从 opus JSON 抽取的链接卡片元数据，共 160 字节。
type biliLinkCardMetadata struct {
	Identity string
	Type     string
	URL      string
	Title    string
	Image    string
	Views    string
	Danmaku  string
	Duration string
	CardTag  string
	Author   string
}

type PostElement struct {
	Ele      string
	Type     string
	Url      string
	Desc     string
	Image    string
	Align    string
	Runs     []PostRun
	Views    string
	Danmaku  string
	Duration string
	CardTag  string
	Author   string
}

func preProcess(oParams []map[string]interface{}) (map[string]interface{}, []requests.Option) {
	var params map[string]interface{}
	if len(oParams) == 0 {
		return nil, nil
	} else if len(oParams) == 1 {
		params = oParams[0]
	} else {
		panic("given more than one params")
	}
	fn := func(key string, f func() []requests.Option) []requests.Option {
		var r []requests.Option

		if _, found := params[key]; found {
			r = f()
			delete(params, key)
		}
		return r
	}

	collectStringSlice := func(i interface{}) []string {
		v := reflect.ValueOf(i)
		if v.Kind() == reflect.String {
			return []string{v.String()}
		}
		return cast.ToStringSlice(i)
	}

	var item = []struct {
		key string
		f   func() []requests.Option
	}{
		{
			DDBOT_REQ_DEBUG,
			func() []requests.Option {
				return []requests.Option{requests.DebugOption()}
			},
		},
		{
			DDBOT_REQ_HEADER,
			func() []requests.Option {
				var result []requests.Option
				var header = collectStringSlice(params[DDBOT_REQ_HEADER])
				for _, h := range header {
					spt := strings.SplitN(h, "=", 2)
					if len(spt) >= 2 {
						result = append(result, requests.HeaderOption(spt[0], spt[1]))
					} else {
						logger.WithField("DDBOT_REQ_HEADER", h).Errorf("invalid header format")
					}
				}
				return result
			},
		},
		{
			DDBOT_REQ_COOKIE,
			func() []requests.Option {
				var result []requests.Option
				var cookie = collectStringSlice(params[DDBOT_REQ_COOKIE])
				for _, c := range cookie {
					spt := strings.SplitN(c, "=", 2)
					if len(spt) >= 2 {
						result = append(result, requests.CookieOption(spt[0], spt[1]))
					} else {
						logger.WithField("DDBOT_REQ_COOKIE", c).Errorf("invalid cookie format")
					}
				}
				return result
			},
		},
		{
			DDBOT_REQ_FETCH,
			func() []requests.Option {
				ifetch := params[DDBOT_REQ_FETCH]
				fetch, ok := ifetch.(string)
				if !ok {
					logger.WithField("DDBOT_REQ_FETCH", ifetch).Errorf("invalid fetch format")
					return nil
				}
				switch strings.ToLower(strings.TrimSpace(fetch)) {
				case "local", "remote", "":
					return nil
				default:
					logger.WithField("DDBOT_REQ_FETCH", fetch).Errorf("invalid fetch mode")
					return nil
				}
			},
		},
		{
			DDBOT_REQ_PROXY,
			func() []requests.Option {
				iproxy := params[DDBOT_REQ_PROXY]
				proxy, ok := iproxy.(string)
				if !ok {
					logger.WithField("DDBOT_REQ_PROXY", iproxy).Errorf("invalid proxy format")
					return nil
				}
				if proxy == "prefer_mainland" {
					return []requests.Option{requests.ProxyOption(proxy_pool.PreferMainland)}
				} else if proxy == "prefer_oversea" {
					return []requests.Option{requests.ProxyOption(proxy_pool.PreferOversea)}
				} else if proxy == "prefer_none" {
					return nil
				} else if proxy == "prefer_any" {
					return []requests.Option{requests.ProxyOption(proxy_pool.PreferAny)}
				} else {
					return []requests.Option{requests.RawProxyOption(proxy)}
				}
			},
		},
		{
			DDBOT_REQ_USER_AGENT,
			func() []requests.Option {
				iua := params[DDBOT_REQ_USER_AGENT]
				ua, ok := iua.(string)
				if !ok {
					logger.WithField("DDBOT_REQ_USER_AGENT", iua).Errorf("invalid ua format")
					return nil
				}
				return []requests.Option{requests.AddUAOption(ua)}
			},
		},
		{
			DDBOT_REQ_TIMEOUT,
			func() []requests.Option {
				itime := params[DDBOT_REQ_TIMEOUT]
				timeStr, ok := itime.(string)
				if !ok {
					logger.WithField("DDBOT_REQ_TIMEOUT", itime).Errorf("invalid timeout format")
					return nil
				}
				timeout, err := time.ParseDuration(timeStr)
				if err != nil {
					logger.WithField("DDBOT_REQ_TIMEOUT", timeStr).Errorf("invalid timeout format")
					return nil
				}
				return []requests.Option{requests.TimeoutOption(timeout)}
			},
		},
		{
			DDBOT_REQ_RETRY,
			func() []requests.Option {
				iretry := params[DDBOT_REQ_RETRY]
				retry, ok := iretry.(int64)
				if !ok {
					logger.WithField("DDBOT_REQ_RETRY", iretry).Errorf("invalid retry format")
					return nil
				}
				return []requests.Option{requests.RetryOption(int(retry))}
			},
		},
	}

	var result = []requests.Option{requests.AddUAOption()}
	for _, i := range item {
		result = append(result, fn(i.key, i.f)...)
	}
	return params, result
}

// executeHTTPGet 是 httpGet 与通知缓存 httpGet 共用的底层实现。
//
// 证据：成品 funcs_request_ext.go:230（@0xb4cda0）——参数为 (url string, oParams
// []map[string]interface{})，返回 (body []byte, err error)；先调 preProcess，
// 再内联 requests.Get 的闭包（requests.go:318）交给 requests.Do。
func executeHTTPGet(url string, oParams []map[string]interface{}) (body []byte, err error) {
	params, opts := preProcess(oParams)
	err = requests.Get(url, params, &body, opts...)
	return
}

func httpGet(url string, oParams ...map[string]interface{}) (body []byte) {
	// 成品会先把每个参数表复制一份再交给 executeHTTPGet：preProcess 会 delete
	// 请求控制键，复制可避免污染调用方共享的 map（httpGet @0xb4cf40 的复制循环）。
	params := make([]map[string]interface{}, len(oParams))
	for i, p := range oParams {
		if p == nil {
			continue
		}
		m := make(map[string]interface{}, len(p))
		for k, v := range p {
			m[k] = v
		}
		params[i] = m
	}
	var err error
	body, err = executeHTTPGet(url, params)
	if err != nil {
		logger.Errorf("template: httpGet error %v", err)
	}
	return
}

// executeHTTPHead 证据：成品 funcs_request_ext.go:244（@0xb4d220），返回
// (requests.RespHeader, error)，RespHeader 以栈返回（112 字节）。
func executeHTTPHead(url string, oParams []map[string]interface{}) (headers requests.RespHeader, err error) {
	params, opts := preProcess(oParams)
	err = requests.Head(url, params, &headers, opts...)
	return
}

func httpHead(url string, oParams ...map[string]interface{}) (headers requests.RespHeader) {
	params := make([]map[string]interface{}, len(oParams))
	for i, p := range oParams {
		if p == nil {
			continue
		}
		m := make(map[string]interface{}, len(p))
		for k, v := range p {
			m[k] = v
		}
		params[i] = m
	}
	var err error
	headers, err = executeHTTPHead(url, params)
	if err != nil {
		logger.Errorf("template: httpHead error %v", err)
	}
	return
}

// executeHTTPPostJSON 证据：成品 funcs_request_ext.go:258（@0xb4d840）。
func executeHTTPPostJSON(url string, oParams []map[string]interface{}) (body []byte, err error) {
	params, opts := preProcess(oParams)
	err = requests.PostJson(url, params, &body, opts...)
	return
}

func httpPostJson(url string, oParams ...map[string]interface{}) (body []byte) {
	params := make([]map[string]interface{}, len(oParams))
	for i, p := range oParams {
		if p == nil {
			continue
		}
		m := make(map[string]interface{}, len(p))
		for k, v := range p {
			m[k] = v
		}
		params[i] = m
	}
	var err error
	body, err = executeHTTPPostJSON(url, params)
	if err != nil {
		logger.Errorf("template: httpPostJson error %v", err)
	}
	return
}

// executeHTTPPostForm 证据：成品 funcs_request_ext.go:272（@0xb4dca0）。
func executeHTTPPostForm(url string, oParams []map[string]interface{}) (body []byte, err error) {
	params, opts := preProcess(oParams)
	err = requests.PostForm(url, params, &body, opts...)
	return
}

func httpPostForm(url string, oParams ...map[string]interface{}) (body []byte) {
	params := make([]map[string]interface{}, len(oParams))
	for i, p := range oParams {
		if p == nil {
			continue
		}
		m := make(map[string]interface{}, len(p))
		for k, v := range p {
			m[k] = v
		}
		params[i] = m
	}
	var err error
	body, err = executeHTTPPostForm(url, params)
	if err != nil {
		logger.Errorf("template: httpPostForm error %v", err)
	}
	return
}

func downloadFile(inUrl string, loPath string, fileName string, oParams ...map[string]interface{}) string {
	// 声明变量
	var (
		Url        *url.URL
		err        error
		localPath  string
		resp       bytes.Buffer
		respHeader requests.RespHeader
	)
	params, opts := preProcess(oParams)
	if opts == nil {
		opts = []requests.Option{
			requests.AddUAOption(),
		}
	}
	// 检查URL
	if inUrl == "" {
		logger.Error("请提供URL进行下载")
		return ""
	} else {
		Url, err = url.Parse(inUrl)
		if err != nil {
			logger.Error("无效的URL")
			return ""
		}
	}
	// 设置下载路径
	if loPath == "" {
		logger.Trace("没有指定下载路径，将使用默认路径")
		localPath = "./downloads"
	} else {
		localPath = loPath
	}
	// 检查文件路径是否存在
	if _, err = os.Stat(localPath); os.IsNotExist(err) {
		if err = os.MkdirAll(localPath, 0755); err != nil {
			logger.Errorf("创建下载目录失败:%v", err)
			return ""
		}
	}
	err = requests.GetWithHeader(Url.String(), params, &resp, &respHeader, opts...)
	if err != nil {
		logger.Errorf("下载文件失败:%v", err)
		return ""
	}
	if fileName == "" {
		if respHeader.ContentDisposition != "" {
			fileName = respHeader.ContentDisposition
		} else {
			var vaild bool
			fileName, vaild = extractFilename(Url.String())
			if fileName == "" || !vaild {
				fileName = uuid.New().String()
			}
		}
	}
	filePath := localPath + "/" + fileName
	err = os.WriteFile(filePath, resp.Bytes(), 0644)
	if err != nil {
		logger.Errorf("保存文件失败:%v", err)
		return ""
	}
	return filePath
}

// 提取文件名并验证有效性，返回 (文件名, 是否有效)
func extractFilename(urlStr string) (string, bool) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", false
	}

	cleanPath := path.Clean(u.Path)
	base := path.Base(cleanPath)

	// 验证文件名有效性（与判断逻辑一致）
	if base == "." || base == ".." || base == "/" || base == "" {
		return "", false
	}

	dotIndex := strings.LastIndex(base, ".")
	if dotIndex == -1 || dotIndex == 0 || dotIndex == len(base)-1 {
		return "", false
	}

	return base, true
}

// getBiliPostBody 证据：成品 funcs_request_ext.go:374（@0xb4eac0）——用
// bytes.Buffer 接收响应体，Options 依次为 AddUAOption / ProxyOption(PreferNone) /
// RetryOption(3)；成功后把 body.Bytes() 复制一份返回（append([]byte(nil), ...)）。
func getBiliPostBody(Url string) ([]byte, error) {
	opts := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
	}
	var body bytes.Buffer
	if err := requests.Get(Url, nil, &body, opts...); err != nil {
		return nil, err
	}
	return append([]byte(nil), body.Bytes()...), nil
}

// getBiliPost 证据：成品 funcs_request_ext.go:388（@0xb4ee80）——失败时
// logger.Errorf("template: parseBiliPost error %v", err) 并返回 nil。
func getBiliPost(Url string) []PostElement {
	body, err := getBiliPostBody(Url)
	if err != nil {
		logger.Errorf("template: parseBiliPost error %v", err)
		return nil
	}
	return parseBiliPostContent(body)
}

// biliParagraphsJSON 从页面字节里截取出 "module_content" 之后 "paragraphs" 对应的 JSON 数组。
// 证据：成品 funcs_request_ext.go:796（@0xb51900）。
func biliParagraphsJSON(data []byte) []byte {
	idx := bytes.Index(data, []byte(`"module_content"`))
	if idx < 0 {
		return nil
	}
	j := bytes.Index(data[idx:], []byte(`"paragraphs"`))
	if j < 0 {
		return nil
	}
	i := idx + j + len(`"paragraphs"`)
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	if i < len(data) && data[i] == ':' {
		i++
	}
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	if i >= len(data) || data[i] != '[' {
		return nil
	}
	start := i
	depth := 0
	inString := false
	escaped := false
	for i < len(data) {
		c := data[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		} else {
			switch c {
			case '"':
				inString = true
			case '[':
				depth++
			case ']':
				if depth == 1 {
					return data[start : i+1]
				}
				depth--
			}
		}
		i++
	}
	return nil
}

// biliParagraphText 汇总一段 paragraph 的纯文本：para_type 1 取 text、4 取
// blockquote 子段（换行连接）、8 取 heading，其余返回空。
// 证据：成品 funcs_request_ext.go:886（@0xb51bc0）。
func biliParagraphText(para biliJSONParagraph) string {
	switch para.ParaType {
	case 1:
		var sb strings.Builder
		if para.Text != nil {
			for _, w := range para.Text.Nodes {
				if w.Word != nil {
					sb.WriteString(w.Word.Words)
				}
			}
		}
		return strings.TrimSpace(sb.String())
	case 4:
		if para.Blockquote == nil {
			return ""
		}
		texts := make([]string, 0, len(para.Blockquote.Children))
		for _, child := range para.Blockquote.Children {
			var sb strings.Builder
			if child.Text != nil {
				for _, w := range child.Text.Nodes {
					if w.Word != nil {
						sb.WriteString(w.Word.Words)
					}
				}
			}
			if t := strings.TrimSpace(sb.String()); t != "" {
				texts = append(texts, t)
			}
		}
		return strings.Join(texts, "\n")
	case 8:
		var sb strings.Builder
		if para.Heading != nil {
			for _, w := range para.Heading.Nodes {
				if w.Word != nil {
					sb.WriteString(w.Word.Words)
				}
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// biliTextAlignMap 建立「段落正文 -> 对齐方式」映射：先遍历顶层段落，para_type==4
// 的再遍历其 blockquote 子段。对齐值取 format.align（无 format 时取 align），
// 1 -> center、2 -> right，其余为空串。
// 证据：成品 funcs_request_ext.go:911（@0xb52420）。
func biliTextAlignMap(data []byte) map[string]string {
	paraJSON := biliParagraphsJSON(data)
	if len(paraJSON) == 0 {
		return nil
	}
	var paras []biliJSONParagraph
	if err := json.Unmarshal(paraJSON, &paras); err != nil {
		return nil
	}
	m := make(map[string]string)
	for _, para := range paras {
		if text := biliParagraphText(para); text != "" {
			align := para.Align
			if para.Format != nil {
				align = para.Format.Align
			}
			name := ""
			switch align {
			case 1:
				name = "center"
			case 2:
				name = "right"
			}
			m[text] = name
		}
		if para.ParaType == 4 && para.Blockquote != nil {
			for _, child := range para.Blockquote.Children {
				var sb strings.Builder
				if child.Text != nil {
					for _, w := range child.Text.Nodes {
						if w.Word != nil {
							sb.WriteString(w.Word.Words)
						}
					}
				}
				text := strings.TrimSpace(sb.String())
				if text == "" {
					continue
				}
				align := child.Align
				if child.Format != nil {
					align = child.Format.Align
				}
				name := ""
				switch align {
				case 1:
					name = "center"
				case 2:
					name = "right"
				}
				m[text] = name
			}
		}
	}
	return m
}

// biliLookupAlign 先按段落文本查对齐表，未命中时回退到 paraAlign 的 style 推断。
// 证据：成品 funcs_request_ext.go:936（@0xb529e0）。
func biliLookupAlign(m map[string]string, sel *goquery.Selection, text string) string {
	if v, ok := m[strings.TrimSpace(text)]; ok {
		return v
	}
	return paraAlign(sel)
}

// replaceAvifWithPng 把 B站图片地址补全协议，并把 .avif/.webp 后缀改成 .png。
// 证据：成品 funcs_request_ext.go:959（@0xb52a80）——仅当后缀命中 .avif/.webp
// 时才把最后 5 字节替换为 ".png"，不会给其它地址追加 .png；
// 另外会把第一个 "http://" 替换成 "https://"。
func replaceAvifWithPng(src string) string {
	if strings.HasPrefix(src, "//") {
		src = "https:" + src
	}
	src = strings.Replace(src, "http://", "https://", 1)
	if strings.HasSuffix(src, ".avif") {
		src = src[:len(src)-5] + ".png"
	} else if strings.HasSuffix(src, ".webp") {
		src = src[:len(src)-5] + ".png"
	}
	return src
}

// toAbsoluteURL 证据：成品 funcs_request_ext.go:974（@0xb52c00）。
func toAbsoluteURL(rel string) string {
	base := "https://i0.hdslb.com"
	if strings.HasPrefix(rel, "//") {
		return "https:" + rel
	}
	if strings.HasPrefix(rel, "/") {
		return base + rel
	}
	return rel
}

// postAttr 读取节点上指定属性的值。证据：成品 funcs_request_ext.go:985（@0xb52ca0）。
func postAttr(sel *goquery.Selection, attr string) string {
	if sel == nil || len(sel.Nodes) == 0 {
		return ""
	}
	node := sel.Nodes[0]
	if node == nil {
		return ""
	}
	for _, a := range node.Attr {
		if a.Key == attr {
			return a.Val
		}
	}
	return ""
}

// firstCardImage 依次尝试 selectors，取首个节点 src 属性非空的地址，最后回退到 img。
// 返回的是未 TrimSpace 的原始属性值（成品只把 TrimSpace 用于判空）。
// 证据：成品 funcs_request_ext.go:990（@0xb52e00）。
func firstCardImage(sel *goquery.Selection, selectors []string) (string, bool) {
	for _, s := range selectors {
		el := sel.Find(s).Eq(0)
		if len(el.Nodes) == 0 {
			continue
		}
		if node := el.Nodes[0]; node != nil {
			for _, a := range node.Attr {
				if a.Key == "src" && strings.TrimSpace(a.Val) != "" {
					return a.Val, true
				}
			}
		}
	}
	el := sel.Find("img").Eq(0)
	if len(el.Nodes) > 0 {
		if node := el.Nodes[0]; node != nil {
			for _, a := range node.Attr {
				if a.Key == "src" && strings.TrimSpace(a.Val) != "" {
					return a.Val, true
				}
			}
		}
	}
	return "", false
}

// firstCardText 证据：成品 funcs_request_ext.go:1002（@0xb530c0）。
func firstCardText(sel *goquery.Selection, selectors []string) string {
	for _, s := range selectors {
		if t := strings.TrimSpace(sel.Find(s).Eq(0).Text()); t != "" {
			return t
		}
	}
	return ""
}

// hexToRGB 证据：成品 funcs_request_ext.go:1011（@0xb53180）——去掉可选 #，
// 长度必须为 6，否则返回三个 0；解析错误被忽略（ParseInt 失败即为 0）。
func hexToRGB(hex string) (int64, int64, int64) {
	hex = strings.TrimSpace(hex)
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return r, g, b
}

// resolveBiliPostColor 把 opus 的 var(--Xxx[, fallback]) / rgba(var(--Xxx_rgb), a)
// 颜色表达式解析成可用颜色。证据：成品 funcs_request_ext.go:1023（@0xb53280）。
func resolveBiliPostColor(color string) string {
	color = strings.TrimSpace(color)
	if strings.HasPrefix(color, "var(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(color, "var("), ")")
		parts := strings.SplitN(inner, ",", 2)
		if v, ok := biliPostColorVars[strings.TrimPrefix(strings.TrimSpace(parts[0]), "--")]; ok {
			return v
		}
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
		return ""
	}
	if strings.HasPrefix(color, "rgba(var(") {
		m := biliRgbaVarRe.FindStringSubmatch(color)
		if len(m) >= 3 {
			if v, ok := biliPostColorVars[strings.TrimSpace(m[1])]; ok {
				r, g, b := hexToRGB(v)
				return fmt.Sprintf("rgba(%d,%d,%d,%s)", r, g, b, strings.TrimSpace(m[2]))
			}
		}
		return "rgba(0,0,0,0.6)"
	}
	return color
}

// colorFromPostStyle 证据：成品 funcs_request_ext.go:1050（@0xb535a0）。
func colorFromPostStyle(style string) string {
	m := biliColorRe.FindStringSubmatch(style)
	if len(m) < 2 {
		return ""
	}
	return resolveBiliPostColor(m[1])
}

// extractPostColor 证据：成品 funcs_request_ext.go:1058（@0xb53620）。
func extractPostColor(sel *goquery.Selection) string {
	if c := colorFromPostStyle(postAttr(sel, "style")); c != "" {
		return c
	}
	return colorFromPostStyle(postAttr(sel.Find("span, strong").Eq(0), "style"))
}

// extractPostFontSize 证据：成品 funcs_request_ext.go:1066（@0xb536c0）——
// 先看 style 里的 font-size，再按 class 上的 opus-font-* 取档位。
func extractPostFontSize(sel *goquery.Selection) int {
	if m := biliFontSizeRe.FindStringSubmatch(postAttr(sel, "style")); len(m) >= 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	class := postAttr(sel, "class")
	switch {
	case strings.Contains(class, "opus-font-xxLarge"):
		return 26
	case strings.Contains(class, "opus-font-xLarge"):
		return 22
	case strings.Contains(class, "opus-font-large"):
		return 20
	case strings.Contains(class, "opus-font-medium"):
		return 18
	case strings.Contains(class, "opus-font-small"):
		return 16
	}
	return 0
}

// paraAlign 从 style 推断对齐方式，style 无对齐信息时看第一个 span/strong。
// 证据：成品 funcs_request_ext.go:1089（@0xb53840）。
func paraAlign(sel *goquery.Selection) string {
	style := strings.ToLower(strings.Replace(postAttr(sel, "style"), " ", "", -1))
	if !strings.Contains(style, "text-align:center") && !strings.Contains(style, "text-align:right") {
		style = strings.ToLower(strings.Replace(postAttr(sel.Find("span, strong").Eq(0), "style"), " ", "", -1))
	}
	if strings.Contains(style, "text-align:center") {
		return "center"
	}
	if strings.Contains(style, "text-align:right") {
		return "right"
	}
	return ""
}

// collectRuns 遍历节点的直接子节点，把文本/br/行内样式节点收集成 []PostRun。
// 证据：成品 funcs_request_ext.go:1103（@0xb539c0）。
func collectRuns(sel *goquery.Selection) []PostRun {
	var runs []PostRun
	contents := sel.Contents()
	for i := 0; i < contents.Length(); i++ {
		el := contents.Eq(i)
		switch goquery.NodeName(el) {
		case "br":
			runs = append(runs, PostRun{Text: "\n"})
		case "#text":
			text := el.Text()
			if text == "" {
				continue
			}
			if strings.TrimSpace(text) == "" && strings.IndexAny(text, "\r\n\t") >= 0 {
				continue
			}
			runs = append(runs, PostRun{Text: text})
		default:
			style := strings.ToLower(postAttr(el, "style"))
			text := el.Text()
			color := extractPostColor(el)
			size := extractPostFontSize(el)
			bold := goquery.NodeName(el) == "strong" || strings.Contains(style, "font-weight:bold")
			if text != "" {
				runs = append(runs, PostRun{Text: text, Color: color, Size: size, Bold: bold})
			}
		}
	}
	return runs
}

// firstNonEmptyLine 证据：成品 funcs_request_ext.go:711（@0xb50fe0）——
// 先把 \r\n 替换成 \n 再按 \n 切分，返回首个 TrimSpace 后非空的行。
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(strings.Replace(s, "\r\n", "\n", -1), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// biliCardIdentity 从链接里提取卡片身份：路径中出现 opus/video 段时返回
// "opus:<id>" / "video:<大写BVID>"，否则返回空串。
// 证据：成品 funcs_request_ext.go:720（@0xb510a0）。
func biliCardIdentity(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "//") {
		rawURL = "https:" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "opus":
			return "opus:" + parts[i+1]
		case "video":
			return "video:" + strings.ToUpper(parts[i+1])
		}
	}
	return ""
}

// biliFindLinkCardMetadata 先按链接身份在 metas 里找同一条，找不到再退化为按下标取。
// 证据：成品 funcs_request_ext.go:744（@0xb51280）。
func biliFindLinkCardMetadata(metas []biliLinkCardMetadata, rawURL string, idx int) (biliLinkCardMetadata, bool) {
	if identity := biliCardIdentity(rawURL); identity != "" {
		for _, m := range metas {
			if m.Identity == identity {
				return m, true
			}
		}
	}
	if idx >= 0 && idx < len(metas) {
		return metas[idx], true
	}
	return biliLinkCardMetadata{}, false
}

// mergeBiliLinkCardMetadata 把 JSON 元数据合并进 PostElement 的空字段；
// Type 特殊：卡片类型为空，或当前是 link-card 而 JSON 是 dynamic-card 时才覆盖。
// 证据：成品 funcs_request_ext.go:758（@0xb51620）。src.Identity 在成品中未被读取。
func mergeBiliLinkCardMetadata(dst *PostElement, src biliLinkCardMetadata) {
	if dst == nil {
		return
	}
	if dst.Type == "" || (dst.Type == "link-card" && src.Type == "dynamic-card") {
		dst.Type = src.Type
	}
	if dst.Url == "" {
		dst.Url = src.URL
	}
	if dst.Ele == "" {
		dst.Ele = src.URL
	}
	if dst.Desc == "" {
		dst.Desc = src.Title
	}
	if dst.Image == "" {
		dst.Image = src.Image
	}
	if dst.Views == "" {
		dst.Views = src.Views
	}
	if dst.Danmaku == "" {
		dst.Danmaku = src.Danmaku
	}
	if dst.Duration == "" {
		dst.Duration = src.Duration
	}
	if dst.CardTag == "" {
		dst.CardTag = src.CardTag
	}
	if dst.Author == "" {
		dst.Author = src.Author
	}
}

// biliLinkCardMetadataFromJSON 从页面 JSON 的 paragraphs 里抽取链接卡片元数据。
// 证据：成品 funcs_request_ext.go:628（@0xb50520）——Eva3Opus 分支 Type 取
// "dynamic-card"（静态字面量 @0x1145840），Eva3Video 分支取 "link-card"
// （@0x11458e0），其余字段赋值顺序与条件见下。
func biliLinkCardMetadataFromJSON(data []byte) []biliLinkCardMetadata {
	paraJSON := biliParagraphsJSON(data)
	if len(paraJSON) == 0 {
		return nil
	}
	var paras []biliJSONParagraph
	if err := json.Unmarshal(paraJSON, &paras); err != nil {
		return nil
	}
	var out []biliLinkCardMetadata
	for _, para := range paras {
		if para.LinkCard == nil || para.LinkCard.Card == nil {
			continue
		}
		card := para.LinkCard.Card
		if card.Eva3Opus != nil && card.Eva3Opus.Info != nil {
			info := card.Eva3Opus.Info
			meta := biliLinkCardMetadata{Type: "dynamic-card"}
			meta.URL = strings.TrimSpace(info.JumpURL)
			meta.Title = strings.TrimSpace(info.Title)
			meta.Views = strings.TrimSpace(info.View)
			meta.Danmaku = strings.TrimSpace(info.Reply)
			meta.CardTag = strings.TrimSpace(info.Badge)
			meta.Author = strings.TrimSpace(info.AuthorName)
			if meta.Title == "" {
				meta.Title = firstNonEmptyLine(info.Content)
			}
			if len(info.Pics) > 0 {
				meta.Image = replaceAvifWithPng(strings.TrimSpace(info.Pics[0].Src))
			}
			if len(info.Pics) > 1 {
				meta.Duration = strconv.Itoa(len(info.Pics))
			}
			if info.CardBadge != nil && info.CardBadge.IconBadge != nil {
				if t := strings.TrimSpace(info.CardBadge.IconBadge.Text); t != "" {
					meta.CardTag = t
				}
			}
			meta.Identity = biliCardIdentity(meta.URL)
			if meta.Identity == "" {
				first := ""
				for _, s := range []string{info.OpusID, card.Type} {
					if strings.TrimSpace(s) != "" {
						first = s
						break
					}
				}
				meta.Identity = "opus:" + strings.TrimSpace(first)
			}
			out = append(out, meta)
		} else if card.Eva3Video != nil && card.Eva3Video.Info != nil {
			info := card.Eva3Video.Info
			meta := biliLinkCardMetadata{Type: "link-card"}
			meta.URL = strings.TrimSpace(info.JumpURL)
			meta.Title = strings.TrimSpace(info.Title)
			meta.Image = replaceAvifWithPng(strings.TrimSpace(info.Cover))
			meta.Author = strings.TrimSpace(info.AuthorName)
			for _, s := range info.StatInfos {
				switch s.Type {
				case 1:
					meta.Views = strings.TrimSpace(s.Text)
				case 2:
					meta.Danmaku = strings.TrimSpace(s.Text)
				}
			}
			if info.VideoTS != nil {
				meta.Duration = strings.TrimSpace(info.VideoTS.DurationText)
			}
			meta.Identity = biliCardIdentity(meta.URL)
			if meta.Identity == "" {
				if bvid := strings.TrimSpace(info.BVID); bvid != "" {
					meta.Identity = "video:" + strings.ToUpper(bvid)
				}
			}
			out = append(out, meta)
		}
	}
	return out
}

// parseBiliPostContent 解析 B站 opus 正文 DOM。证据：成品 funcs_request_ext.go:397
// （@0xb4ef20）；内部闭包位于 432/437/507 行。处理顺序：封面图、合集信息、
// 正文子节点（段落/标题/图片/链接卡片/引用块）。
func parseBiliPostContent(data []byte) []PostElement {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var content []PostElement
	metas := biliLinkCardMetadataFromJSON(data)
	cardIdx := 0
	aligns := biliTextAlignMap(data)

	// .opus-module-top__album__cover img 的 src 作为封面图元素。
	if cover := doc.Find(".opus-module-top__album__cover img"); len(cover.Nodes) > 0 {
		if node := cover.Nodes[0]; node != nil {
			for _, a := range node.Attr {
				if a.Key == "src" {
					if img := replaceAvifWithPng(a.Val); img != "" {
						content = append(content, PostElement{
							Ele:  img,
							Type: "image",
							Desc: "top__album__cover",
						})
					}
					break
				}
			}
		}
	}

	// 合集信息。
	if collectionCount := extractBiliCollectionCount(data); collectionCount != "" {
		content = append(content, PostElement{
			Ele:  collectionCount,
			Type: "collection",
			Url:  extractBiliCollectionUrl(data),
		})
	}

	doc.Find(".opus-module-content").Each(func(_ int, s *goquery.Selection) {
		s.Children().Each(func(_ int, e *goquery.Selection) {
			var ele PostElement
			switch goquery.NodeName(e) {
			case "h1", "h2", "p", "h3":
				text := strings.TrimSpace(e.Text())
				if text != "" {
					ele.Ele = text
					ele.Type = "text"
					ele.Align = biliLookupAlign(aligns, e, text)
					ele.Runs = collectRuns(e)
				}
			case "hr":
				return
			case "div":
				switch {
				case e.HasClass("opus-para-pic"):
					if src, exists := e.Find("img").Attr("src"); exists {
						fullUrl := toAbsoluteURL(src)
						if fullUrl != "" {
							ele.Type = "image"
							ele.Ele = fullUrl
						}
					}
					if captionText := strings.TrimSpace(e.Find(".opus-pic-view__caption").Text()); captionText != "" {
						ele.Desc = captionText
					}
				case e.HasClass("opus-para-link-card"):
					if e.Find(".opus-tag.tag-dynamic").Length() > 0 {
						ele.Type = "dynamic-card"
					} else {
						ele.Type = "link-card"
					}
					if href, exists := e.Find("a").Attr("href"); exists {
						ele.Ele = href
						ele.Url = href
					}
					ele.Desc = firstCardText(e, []string{".video-card-detailed-title", ".opus-title"})
					if img, ok := firstCardImage(e, []string{".video-thumbnail", ".opus-cover img"}); ok {
						if png := replaceAvifWithPng(img); png != "" {
							ele.Image = png
						}
					}
					ele.CardTag = strings.TrimSpace(e.Find(".opus-tag").First().Text())
					ele.Author = strings.TrimSpace(e.Find(".opus-author").Text())
					ele.Views = strings.TrimSpace(e.Find(".play-count").Text())
					ele.Danmaku = strings.TrimSpace(e.Find(".danmaku-count").Text())
					if ele.Views == "" || ele.Danmaku == "" {
						e.Find(".opus-stats .stat-item").Each(func(_ int, stat *goquery.Selection) {
							statText := strings.TrimSpace(stat.Text())
							if ele.Views == "" && strings.Contains(statText, "阅读") {
								ele.Views = statText
							}
							if ele.Danmaku == "" && strings.Contains(statText, "评论") {
								ele.Danmaku = statText
							}
						})
					}
					ele.Duration = strings.TrimSpace(e.Find(".video-duration, .opus-badge").First().Text())
					if meta, ok := biliFindLinkCardMetadata(metas, ele.Url, cardIdx); ok {
						mergeBiliLinkCardMetadata(&ele, meta)
					}
					cardIdx++
				}
			case "blockquote":
				handled := false
				e.ChildrenFiltered("p").Each(func(_ int, p *goquery.Selection) {
					text := strings.TrimSpace(p.Text())
					if text == "" {
						return
					}
					content = append(content, PostElement{
						Ele:   text,
						Type:  "text",
						Align: biliLookupAlign(aligns, p, text),
						Runs:  collectRuns(p),
					})
					handled = true
				})
				if handled {
					return
				}
				text := strings.TrimSpace(e.Text())
				if text == "" {
					return
				}
				content = append(content, PostElement{
					Ele:   text,
					Type:  "text",
					Align: biliLookupAlign(aligns, e, text),
					Runs:  collectRuns(e),
				})
				return
			}
			if ele.Type != "" {
				content = append(content, ele)
			}
		})
	})
	return content
}

func extractBiliCollectionCount(data []byte) string {
	matches := biliCollectionCountRe.FindSubmatch(data)
	if len(matches) < 2 {
		return ""
	}
	return string(matches[1])
}

func extractBiliCollectionUrl(data []byte) string {
	matches := biliCollectionIdRe.FindSubmatch(data)
	if len(matches) < 2 {
		return ""
	}
	return "https://www.bilibili.com/read/readlist/rl" + string(matches[1])
}

// 常见文件头定义（魔数）
var fileSignatures = map[string][]byte{
	// 图片
	"PNG":  {0x89, 0x50, 0x4E, 0x47}, // \x89PNG
	"JPG":  {0xFF, 0xD8, 0xFF},       // JPEG
	"GIF":  {0x47, 0x49, 0x46, 0x38}, // GIF8
	"BMP":  {0x42, 0x4D},             // BM
	"TIFF": {0x49, 0x49, 0x2A, 0x00}, // II*\x00 或者 MM\x00*
	"PSD":  {0x38, 0x42, 0x50, 0x53}, // 8BPS

	// 文档
	"PDF":  {0x25, 0x50, 0x44, 0x46},       // %PDF
	"RTF":  {0x7B, 0x5C, 0x72, 0x74, 0x66}, // {\rtf
	"DOC":  {0xD0, 0xCF, 0x11, 0xE0},       // OLE Compound
	"XLS":  {0xD0, 0xCF, 0x11, 0xE0},       // 同上
	"PPT":  {0xD0, 0xCF, 0x11, 0xE0},       // 同上
	"DOCX": {0x50, 0x4B, 0x03, 0x04},       // ZIP 格式
	"XLSX": {0x50, 0x4B, 0x03, 0x04},       // ZIP 格式
	"PPTX": {0x50, 0x4B, 0x03, 0x04},       // ZIP 格式

	// 压缩
	"ZIP":   {0x50, 0x4B, 0x03, 0x04},             // PK..
	"RAR":   {0x52, 0x61, 0x72, 0x21},             // Rar!
	"7Z":    {0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}, // 7z..
	"GZIP":  {0x1F, 0x8B},                         // gzip
	"BZIP2": {0x42, 0x5A, 0x68},                   // BZh

	// 音视频
	"MP3":  {0x49, 0x44, 0x33},                               // ID3
	"WAV":  {0x52, 0x49, 0x46, 0x46},                         // RIFF
	"AVI":  {0x52, 0x49, 0x46, 0x46},                         // RIFF
	"MP4":  {0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70}, // ftyp
	"MIDI": {0x4D, 0x54, 0x68, 0x64},                         // MThd

	// 可执行
	"EXE":   {0x4D, 0x5A},             // MZ
	"DLL":   {0x4D, 0x5A},             // MZ
	"ELF":   {0x7F, 0x45, 0x4C, 0x46}, // ELF
	"MachO": {0xFE, 0xED, 0xFA, 0xCE}, // Mach-O
	"ICO":   {0x00, 0x00, 0x01, 0x00}, // ICO
}

// DetectFileType 检测文件头
func DetectFileType(data []uint8) string {
	for typ, sig := range fileSignatures {
		if len(data) >= len(sig) && bytes.Equal(data[:len(sig)], sig) {
			return typ
		}
	}
	return "UNKNOWN"
}
