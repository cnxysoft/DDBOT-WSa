package template

import (
	"bytes"
	"net/url"
	"os"
	"path"
	"reflect"
	"regexp"
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
	biliCollectionIdRe   = regexp.MustCompile(`"collection_id"\s*:\s*(\d+)`)
)

const (
	DDBOT_REQ_DEBUG      = "DDBOT_REQ_DEBUG"
	DDBOT_REQ_HEADER     = "DDBOT_REQ_HEADER"
	DDBOT_REQ_COOKIE     = "DDBOT_REQ_COOKIE"
	DDBOT_REQ_PROXY      = "DDBOT_REQ_PROXY"
	DDBOT_REQ_USER_AGENT = "DDBOT_REQ_USER_AGENT"
	DDBOT_REQ_TIMEOUT    = "DDBOT_REQ_TIMEOUT"
	DDBOT_REQ_RETRY      = "DDBOT_REQ_RETRY"
)

type PostElement struct {
	Ele   string
	Type  string
	Url   string
	Desc  string
	Image string
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

func httpGet(url string, oParams ...map[string]interface{}) (body []byte) {
	params, opts := preProcess(oParams)
	err := requests.Get(url, params, &body, opts...)
	if err != nil {
		logger.Errorf("template: httpGet error %v", err)
	}
	return
}

func httpHead(url string, oParams ...map[string]interface{}) (headers requests.RespHeader) {
	params, opts := preProcess(oParams)
	err := requests.Head(url, params, &headers, opts...)
	if err != nil {
		logger.Errorf("template: httpHead error %v", err)
	}
	return
}

func httpPostJson(url string, oParams ...map[string]interface{}) (body []byte) {
	params, opts := preProcess(oParams)
	err := requests.PostJson(url, params, &body, opts...)
	if err != nil {
		logger.Errorf("template: httpGet error %v", err)
	}
	return
}

func httpPostForm(url string, oParams ...map[string]interface{}) (body []byte) {
	params, opts := preProcess(oParams)
	err := requests.PostForm(url, params, &body, opts...)
	if err != nil {
		logger.Errorf("template: httpGet error %v", err)
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

func getBiliPost(Url string) []PostElement {
	opts := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
	}
	var body bytes.Buffer
	err := requests.Get(Url, nil, &body, opts...)
	if err != nil {
		return nil
	}
	return parseBiliPostContent(body.Bytes())
}

func parseBiliPostContent(data []byte) []PostElement {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var content []PostElement

	// Extract collection info from __INITIAL_STATE__ and prepend it
	collectionCount := extractBiliCollectionCount(data)
	collectionUrl := extractBiliCollectionUrl(data)
	if collectionCount != "" {
		content = append(content, PostElement{
			Ele:  collectionCount,
			Type: "collection",
			Url:  collectionUrl,
		})
	}

	doc.Find(".opus-module-content").Each(func(i int, s *goquery.Selection) {
		s.Children().Each(func(_ int, e *goquery.Selection) {
			var ele PostElement
			switch goquery.NodeName(e) {
			case "h1", "h2", "p", "blockquote":
				text := strings.TrimSpace(e.Text())
				if text != "" {
					ele.Ele = text
					ele.Type = "text"
				}
			case "hr":
				return
			case "div":
				if e.HasClass("opus-para-pic") {
					img := e.Find("img")
					if src, exists := img.Attr("src"); exists {
						fullUrl := toAbsoluteURL(src)
						if fullUrl != "" {
							ele.Type = "image"
							ele.Ele = fullUrl
						}
					}
					caption := e.Find(".opus-pic-view__caption")
					if captionText := strings.TrimSpace(caption.Text()); captionText != "" {
						ele.Desc = captionText
					}
				} else if e.HasClass("opus-para-link-card") {
					isDynamic := e.Find(".opus-tag.tag-dynamic").Length() > 0
					if isDynamic {
						ele.Type = "dynamic-card"
					} else {
						ele.Type = "link-card"
					}
					link := e.Find("a")
					if href, exists := link.Attr("href"); exists {
						ele.Ele = href
					}
					title := e.Find(".opus-title")
					if linkText := strings.TrimSpace(title.Text()); linkText != "" {
						ele.Desc = linkText
					}
					cover := e.Find(".opus-cover img")
					if src, exists := cover.Attr("src"); exists {
						imgUrl := replaceAvifWithPng(src)
						if imgUrl != "" {
							ele.Image = imgUrl
						}
					}
				}
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

func replaceAvifWithPng(src string) string {
	if strings.HasPrefix(src, "//") {
		src = "https:" + src
	}
	if !strings.HasSuffix(src, ".png") {
		src += ".png"
	}
	return src
}

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
