package douyin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
)

// messageLinkParser 是抖音聊天链接解析器，
// 实现 lsp 消息链接解析框架（lsp/link_parser.go）的接口方法面：
// Name/DisplayName/Enabled/Allowed/Resolve/Render/Timeout/DedupWindow。
// 注意：框架本体属 lsp 包（并行范围），本文件只提供具体实现与配置读取。
type messageLinkParser struct{}

// douyinMessageLinkParser 是包级单例。
var douyinMessageLinkParser = &messageLinkParser{}

// init 注册抖音链接解析器（lsp 框架按 Name 去重）。
// 证据：成品存在 douyin.init.2 @ message_link_parser.go:19（Lite 增量），
// 重建产物此前缺失；init.go 的 concern 注册为独立的另一个 init，互不冲突。
func init() {
	lsp.RegisterMessageLinkParser(douyinMessageLinkParser)
}

// Name 解析器注册名 [推断]。
// 成品 message_link_parser.go 中方法为值接收者（同时存在 messageLinkParser.Name
// 与 (*messageLinkParser).Name 两个符号），此处保持值接收者。
func (p messageLinkParser) Name() string {
	return "douyin-link-parser"
}

// DisplayName 展示名 [推断]。
func (p messageLinkParser) DisplayName() string {
	return "抖音链接"
}

// Enabled 判断当前会话是否启用抖音链接解析。
// 参数 private 表示会话是否为私聊（框架 MessageLinkParser.Enabled(private bool) bool）。
// 总开关 douyin.linkParse；私聊还需 douyin.linkParsePrivate。
// 依据：DOUYIN_GUIDE「linkParse: false 群聊和私聊都不解析；linkParsePrivate: false 只解析群聊」。
// 私聊开关放在 Enabled 而非 Allowed，与框架"先总开关后鉴权"的调用顺序一致。
//
// 成品证据（douyin.messageLinkParser.Enabled @0xd8dbc0，312B）：
//   - GlobalConfig==nil -> false（+0x23）；
//   - IsSet+GetBool("douyin.linkParse") 任一 false -> false（call +0x40/+0x63）；
//   - private 时 IsSet+GetBool("douyin.linkParsePrivate") 任一 false -> false（call +0x8b/+0xaa）；
//   - 然后 call cfg.GetEnabledSites @0xae7840（+0xb3），test %dil（+0xb8）：
//     !configured -> true；configured 时遍历返回切片，strings.EqualFold(item,"douyin")
//     命中返回 true，否则 false。
func (p messageLinkParser) Enabled(private bool) bool {
	if config.GlobalConfig == nil {
		return false
	}
	if !config.GlobalConfig.IsSet("douyin.linkParse") || !config.GlobalConfig.GetBool("douyin.linkParse") {
		return false
	}
	if private {
		if !config.GlobalConfig.IsSet("douyin.linkParsePrivate") || !config.GlobalConfig.GetBool("douyin.linkParsePrivate") {
			return false
		}
	}
	enabledSites, configured := cfg.GetEnabledSites()
	if !configured {
		// sites.enabled 未配置：兼容旧配置，全部站点启用。
		return true
	}
	for _, site := range enabledSites {
		if strings.EqualFold(site, "douyin") {
			return true
		}
	}
	return false
}

// Allowed 判断当前会话是否允许解析：
// 群聊满足"群或用户任一白名单"即可；私聊只看用户白名单；两组白名单均为空时不限制。
// 依据：DOUYIN_GUIDE「群聊：群号命中 linkParseGroupWhitelist，或发送者QQ命中
// linkParseUserWhitelist，任一成立即可；私聊：只检查 linkParseUserWhitelist」。
func (p messageLinkParser) Allowed(private bool, groupCode int64, userID int64) bool {
	groupWhitelist := configuredQQWhitelist("douyin.linkParseGroupWhitelist")
	userWhitelist := configuredQQWhitelist("douyin.linkParseUserWhitelist")
	if len(groupWhitelist) == 0 && len(userWhitelist) == 0 {
		return true
	}
	if private {
		return int64InList(userID, userWhitelist)
	}
	if int64InList(groupCode, groupWhitelist) {
		return true
	}
	return int64InList(userID, userWhitelist)
}

// configuredQQWhitelist 读取 QQ 白名单配置（群号/用户号列表）。
func configuredQQWhitelist(key string) []int64 {
	if config.GlobalConfig == nil {
		return nil
	}
	raw := config.GlobalConfig.Get(key)
	items, ok := raw.([]interface{})
	if !ok {
		for _, s := range config.GlobalConfig.GetStringSlice(key) {
			items = append(items, s)
		}
	}
	var result []int64
	for _, item := range items {
		switch v := item.(type) {
		case int64:
			result = append(result, v)
		case int:
			result = append(result, int64(v))
		case float64:
			result = append(result, int64(v))
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				result = append(result, n)
			}
		}
	}
	return result
}

// int64InList 判断值是否在列表中。
func int64InList(v int64, list []int64) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// Timeout 单条链接解析超时（含媒体下载）（成品 messageLinkParser.Timeout 0xd8df00）：
// 读取 douyin.requestTimeout，<=0 取默认 15s，下限 5s，再额外加 5s。
// 注意成品此方法为值接收者（同时存在 (*messageLinkParser).Timeout 包装）。
func (p messageLinkParser) Timeout() time.Duration {
	d := time.Duration(0)
	if config.GlobalConfig != nil {
		d = config.GlobalConfig.GetDuration("douyin.requestTimeout")
	}
	if d <= 0 {
		d = 15 * time.Second
	}
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	return d + 5*time.Second
}

// DedupWindow 重复链接去重窗口（douyin.linkParseDedup，限制在5s到1h）。
// 成品此方法为值接收者（messageLinkParser.DedupWindow 0xd8df60）。
func (p messageLinkParser) DedupWindow() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.linkParseDedup"); v > 0 {
			if v < 5*time.Second {
				return 5 * time.Second
			}
			if v > time.Hour {
				return time.Hour
			}
			return v
		}
	}
	return time.Minute
}

// Resolve 解析链接：先取目标，再拉详情。
// 返回 (规范化链接, 解析详情, 是否命中, 错误)。
// 非抖音链接不命中也不报错，避免聊天中每个非抖音链接都触发一次"链接解析失败"回执；
// ResolveLink 内部短链跟随失败也归入"不命中"（[推断]，成品无此分支的直接证据）。
func (p messageLinkParser) Resolve(ctx context.Context, rawLink string) (string, interface{}, bool, error) {
	target, err := ResolveLink(rawLink)
	if err != nil {
		return "", nil, false, nil
	}
	// FetchLinkDetail 成品按值收 LinkTarget（见 link_parser.go FetchLinkDetail 注释）。
	detail, err := FetchLinkDetail(*target)
	if err != nil {
		return target.URL, nil, false, err
	}
	return target.URL, detail, true, nil
}

// Render 渲染详情为消息。
func (p messageLinkParser) Render(ctx context.Context, detail interface{}) (*mmsg.MSG, error) {
	linkDetail, ok := detail.(*LinkDetail)
	if !ok || linkDetail == nil {
		return nil, errors.New("内部错误：抖音链接目标类型无效")
	}
	m := buildDouyinLinkMessage(linkDetail)
	if m == nil {
		return nil, errors.New("链接解析结果为空")
	}
	return m, nil
}

// getDouyinLinkMaxImages 图文最多取多少张（默认9，限制1到20）。
func getDouyinLinkMaxImages() int {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetInt("douyin.linkParseMaxImages"); v > 0 {
			if v > 20 {
				v = 20
			}
			return v
		}
	}
	return 9
}

// shouldSendDouyinLinkVideo 视频作品是否在文字、封面、原链接之后再单独发送视频。
func shouldSendDouyinLinkVideo() bool {
	if config.GlobalConfig != nil {
		return config.GlobalConfig.GetBool("douyin.linkParseSendVideo")
	}
	return true
}

// appendDouyinLinkURL 把原始链接追加到消息尾部。
func appendDouyinLinkURL(m *mmsg.MSG, linkURL string) *mmsg.MSG {
	if m == nil || linkURL == "" {
		return m
	}
	return m.Text("\n" + linkURL)
}

// buildDouyinGalleryForward 把图集打包成合并转发节点。
func buildDouyinGalleryForward(items []DouyinGalleryItem, name string, maxImages int) *mmsg.ForwardElement {
	nodes := make([]map[string]interface{}, 0, len(items))
	uin := ""
	if bot.Instance != nil {
		uin = strconv.FormatInt(bot.Instance.Uin, 10)
	}
	for i, item := range items {
		if maxImages > 0 && i >= maxImages {
			break
		}
		var content []map[string]interface{}
		if item.ImageURL != "" {
			content = append(content, map[string]interface{}{
				"type": "image",
				"data": map[string]interface{}{"file": item.ImageURL, "url": item.ImageURL},
			})
		}
		if item.VideoURL != "" {
			// 成品 @0xd8f147/@0xaa7 用 concatstring3("\n[", label, "]") 生成节点内文字；
			// 普通短片 label="短片"，实况图 label="Live Photo 动态部分"（item.Kind=="livephoto"，
			// @0xd8f0a6 比较 9 字节小写串 "livephoto"）。
			label := "短片"
			if item.Kind == "livephoto" {
				label = "Live Photo 动态部分"
			}
			content = append(content, map[string]interface{}{
				"type": "text",
				"data": map[string]interface{}{"text": "\n[" + label + "]"},
			})
			content = append(content, map[string]interface{}{
				"type": "video",
				"data": map[string]interface{}{"file": item.VideoURL, "url": item.VideoURL},
			})
		}
		if len(content) == 0 {
			continue
		}
		nodes = append(nodes, map[string]interface{}{
			"type": "node",
			"data": map[string]interface{}{
				"uin": uin,
				// 成品 @0xd8edb6 循环内 fmt.Sprintf("第 %d 张图片", i+1) 作为节点昵称。
				"nickname": fmt.Sprintf("第 %d 张图片", i+1),
				"content":  content,
			},
		})
	}
	if len(nodes) == 0 {
		return nil
	}
	// 成品 @0xd8eeb5/0xd8eefc：fmt.Sprintf("共%d张图片", n) 与字面量 "抖音图集 · "(len16)
	// 拼接后写入新建的 *mmsg.ForwardOptions(+0x0/+0x8，即 Prompt)；@0xd8e171 再把
	// name+"的抖音图集"（concatstring2，+0x8b3/+0x8c0）写入 options 的第二个字符串字段
	//（Source/Summary；重建取 Source）。n 取 len(items)。
	options := &mmsg.ForwardOptions{
		Prompt: "抖音图集 · " + fmt.Sprintf("共%d张图片", len(items)),
		Source: name + "的抖音图集",
	}
	return mmsg.NewForwardElementWithOptions(nodes, options)
}

// buildDouyinLinkMessage 渲染抖音链接详情。
// 成品 @0xd8f4e0（7936B，message_link_parser.go:222）按 detail.Kind 三分支：
//   - "live"   @0xd8fd5b：先追加 "抖音直播间"(15B)，此后 "\n主播：\n"+Name(+0x30/38)、
//     "\n标题：\n"+Title(+0x60/68)、StatusKnown(+0xd9) 时 "\n状态：\n"+
//     (Living(+0xd8)? "直播中":"未开播")、"\n人气：\n"+ViewerText(+0xe0/e8)
//     （标签 rodata 0x10a7705/0x10a770f/0x10a7719/0x10a7723，状态值 0x10a60d1/0x10a60da）；
//   - "work"   @0xd8f557：先追加 detail.Title(+0x60/68)，此后 "\n作者：\n"+Name、
//     CreatedAt(+0xc0/c8/d0) 非零时 "\n发布时间："(0x10af605,len16) +
//     Format("2006-01-02 15:04:05")(0x10b3678,len19)、"\n文案：\n"+Description(0x10a76f1/0x10a76fb)；
//   - "profile"@0xd9046a：先追加 "抖音用户"(12B)，此后 "\n昵称：\n"+Name、
//     "\n抖音号："(0x10ab729,len13)+DisplayID(+0x40/48)、"\n简介：\n"+Description
//     (0x10a772d/0x10a7737)；
//   - 其余 Kind → 返回 nil（@0xd90a07）。
//
// 三种标签都以 "\n" 起头，故类型头本身不带换行，也不再有重建旧版的
// "抖音直播/图文/图集" 复合头（"抖音图集 · 共%d张图片" 改放进 buildDouyinGalleryForward）。
// 下方媒体/原链接处理沿用重建的共用近似（[推断]）。
func buildDouyinLinkMessage(detail *LinkDetail) *mmsg.MSG {
	if detail == nil {
		return nil
	}
	m := mmsg.NewMSG()
	switch detail.Kind {
	case LinkKindLive:
		m.Text("抖音直播间")
		if detail.Name != "" {
			m.Text("\n主播：\n" + detail.Name)
		}
		if detail.Title != "" {
			m.Text("\n标题：\n" + detail.Title)
		}
		if detail.StatusKnown {
			status := "未开播"
			if detail.Living {
				status = "直播中"
			}
			m.Text("\n状态：\n" + status)
		}
		if detail.ViewerText != "" {
			m.Text("\n人气：\n" + detail.ViewerText)
		}
	case LinkKindWork:
		if detail.Title != "" {
			m.Text(detail.Title)
		}
		if detail.Name != "" {
			m.Text("\n作者：\n" + detail.Name)
		}
		if !detail.CreatedAt.IsZero() {
			m.Text("\n发布时间：" + detail.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		if detail.Description != "" {
			m.Text("\n文案：\n" + detail.Description)
		}
	case LinkKindProfile:
		m.Text("抖音用户")
		if detail.Name != "" {
			m.Text("\n昵称：\n" + detail.Name)
		}
		if detail.DisplayID != "" {
			m.Text("\n抖音号：" + detail.DisplayID)
		}
		if detail.Description != "" {
			m.Text("\n简介：\n" + detail.Description)
		}
	default:
		return nil
	}
	// 图集/图片。封面媒体标记按作品类型区分（成品 buildDouyinLinkMessage：
	// [封面]@0x10a4b46 / [直播封面]@0x10acc47 / [头像]@0x10a4b5e）。
	coverAlternative := ""
	switch detail.Kind {
	case LinkKindWork:
		coverAlternative = "[封面]"
	case LinkKindLive:
		coverAlternative = "[直播封面]"
	case LinkKindProfile:
		coverAlternative = "[头像]"
	}
	maxImages := getDouyinLinkMaxImages()
	if len(detail.GalleryItems) > 0 {
		if len(detail.GalleryItems) > maxImages {
			forward := buildDouyinGalleryForward(detail.GalleryItems, detail.Name, maxImages)
			if forward != nil {
				m.Append(forward)
				// 成品 @0xd8fc45/0xd8fcac：图片总数与已转发张数说明。
				m.Textf("\n图片：共%d张，已整理为合并转发", len(detail.GalleryItems))
				m.Textf("（转发前%d张）", maxImages)
				m.Textf("（其余%d张图片请打开原链接查看）\n", len(detail.GalleryItems)-maxImages)
			}
		} else {
			for _, item := range detail.GalleryItems {
				if item.ImageURL != "" {
					m.ImageByUrl(item.ImageURL, "")
				}
				if item.VideoURL != "" && shouldSendDouyinLinkVideo() {
					videoAlternative := "[短片]"
					if item.Kind == "livephoto" {
						videoAlternative = "[Live Photo 动态部分]"
					}
					video := mmsg.NewVideoByUrl(item.VideoURL)
					video.Alternative(videoAlternative)
					m.Append(video)
				}
			}
		}
	} else if len(detail.Images) > 0 {
		count := len(detail.Images)
		if count > maxImages {
			count = maxImages
		}
		for i := 0; i < count; i++ {
			m.ImageByUrl(detail.Images[i], "")
		}
		if len(detail.Images) > maxImages {
			m.Textf("（其余%d张图片请打开原链接查看）\n", len(detail.Images)-maxImages)
		}
	} else if detail.Cover != "" {
		m.ImageByUrl(detail.Cover, coverAlternative)
	}
	// 视频单独发送（失败不影响前面的解析结果）。
	if detail.VideoURL != "" && shouldSendDouyinLinkVideo() && len(detail.GalleryItems) == 0 {
		video := mmsg.NewVideoByUrl(detail.VideoURL)
		video.Alternative("[短片]")
		m.Append(video)
	}
	// 原链接。
	appendDouyinLinkURL(m, detail.URL)
	return m
}
