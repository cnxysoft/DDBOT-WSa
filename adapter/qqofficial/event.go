package qqofficial

// 本文件是**入站事件**的解析层：把官方网关推来的 JSON 翻成 adapter 包的事件结构。
//
// 字段依据全部来自对真实网关推送的实测，不照文档示例写：
//   - 群事件同时带 group_openid 与 group_id（同值，文档只写了前者）；
//   - author 里 member_openid 与 id 同值，union_openid 实测是空串；
//   - 群 @ 消息的 content 去掉 @ 前缀后残留一个前导空格（实测 " hi"）；
//   - 全量模式下其他成员的 @ 被替换成 <@member_openid>；
//   - message_scene.ext 是 ["msg_idx=REFIDX_…","auth_token=…"] 形态的字符串数组。

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// wsAuthor 是事件里的发送者。
type wsAuthor struct {
	ID           string `json:"id"`
	UnionOpenID  string `json:"union_openid"`
	UserOpenID   string `json:"user_openid"`
	MemberOpenID string `json:"member_openid"`
	Username     string `json:"username"`
	MemberRole   string `json:"member_role"`
	Bot          bool   `json:"bot"`
}

// identity 返回这个作者在 (appID) 维度下的稳定身份串。
//
// 优先级：union_openid（跨场景同一人；实测为空串）＞ member_openid（群内身份）＞
// user_openid（单聊身份）＞ id。
// 实测里 author.id 与 member_openid/user_openid 同值，且同一个人在个人机器人下的
// 群内 member_openid 与单聊 user_openid 出现过同一个值（1673FE4A…），所以这几个
// 候选共用同一份 IDMap 命名空间：相等时会自动归一到同一个内部 ID。
func (a wsAuthor) identity() string {
	return firstNonEmpty(a.UnionOpenID, a.MemberOpenID, a.UserOpenID, a.ID)
}

// wsAttachment 是消息附件（图片/语音/视频/文件都走这里）。
type wsAttachment struct {
	URL         string `json:"url"`
	FileName    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// wsMention 是被 @ 的成员（实测不含机器人自己）。
type wsMention struct {
	ID           string `json:"id"`
	MemberOpenID string `json:"member_openid"`
	Username     string `json:"username"`
	MemberRole   string `json:"member_role"`
	IsYou        bool   `json:"is_you"`
	Bot          bool   `json:"bot"`
}

func (m wsMention) identity() string { return firstNonEmpty(m.MemberOpenID, m.ID) }

// wsMessageScene 是 message_scene，ext 形如 ["msg_idx=REFIDX_…","auth_token=…"]。
type wsMessageScene struct {
	Ext    []string `json:"ext"`
	Source string   `json:"source"`
}

// value 取 ext 里 "key=value" 的 value。
func (s wsMessageScene) value(key string) string {
	prefix := key + "="
	for _, item := range s.Ext {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

// wsMessageElement 是 msg_elements 的递归节点。
//
// 只声明承载文本/引用索引的字段：全量群消息的正文可能不在顶层 content 里，
// 而在 msg_elements 里（引用消息 msg_type=103 的 msg_idx 也在这里）。
type wsMessageElement struct {
	Content     string             `json:"content"`
	Text        string             `json:"text"`
	MsgIdx      string             `json:"msg_idx"`
	MsgElements []wsMessageElement `json:"msg_elements"`
}

// wsMessagePayload 是群/单聊消息事件的共同事件体。
type wsMessagePayload struct {
	ID           string             `json:"id"`
	Content      string             `json:"content"`
	GroupOpenID  string             `json:"group_openid"`
	GroupID      string             `json:"group_id"` // 实测多出的字段，与 group_openid 同值
	Timestamp    string             `json:"timestamp"`
	MessageType  int                `json:"message_type"`
	Author       wsAuthor           `json:"author"`
	Attachments  []wsAttachment     `json:"attachments"`
	Mentions     []wsMention        `json:"mentions"`
	MessageScene wsMessageScene     `json:"message_scene"`
	MsgElements  []wsMessageElement `json:"msg_elements"`
}

// groupOpenid 取群 openid：优先 group_openid，缺失时回落实测同值的 group_id。
func (p *wsMessagePayload) groupOpenid() string {
	return firstNonEmpty(p.GroupOpenID, p.GroupID)
}

// selfRefIdx 返回**本条消息自己**的引用索引（REFIDX_ 形态），用于让后续消息引用它。
func (p *wsMessagePayload) selfRefIdx() string {
	if v := p.MessageScene.value("msg_idx"); v != "" {
		return v
	}
	// 引用消息（msg_type=103）的索引在 msg_elements 里
	var walk func(els []wsMessageElement) string
	walk = func(els []wsMessageElement) string {
		for _, el := range els {
			if el.MsgIdx != "" {
				return el.MsgIdx
			}
			if v := walk(el.MsgElements); v != "" {
				return v
			}
		}
		return ""
	}
	return walk(p.MsgElements)
}

// quotedRefIdx 返回本条消息**引用的那条消息**的引用索引。
func (p *wsMessagePayload) quotedRefIdx() string { return p.MessageScene.value("ref_msg_idx") }

// text 取正文：顶层 content 优先，为空时从 msg_elements 里掏。
func (p *wsMessagePayload) text() string {
	if strings.TrimSpace(p.Content) != "" {
		return p.Content
	}
	var parts []string
	var walk func(els []wsMessageElement)
	walk = func(els []wsMessageElement) {
		for _, el := range els {
			if el.Content != "" {
				parts = append(parts, el.Content)
			} else if el.Text != "" {
				parts = append(parts, el.Text)
			}
			walk(el.MsgElements)
		}
	}
	walk(p.MsgElements)
	return strings.Join(parts, "")
}

// timeUnix 把 RFC3339 的 timestamp 转成 unix 秒；解析不了就用当前时间（宁可时间戳不准，
// 也不要给上层一个 0，0 会被限流/去重逻辑当成「很久以前」）。
func (p *wsMessagePayload) timeUnix() int64 {
	if p.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
			return ts.Unix()
		}
		if n, err := strconv.ParseInt(p.Timestamp, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return time.Now().Unix()
}

// eventParser 把官方事件体翻成 DDBOT 事件结构。
type eventParser struct {
	appID string
	ids   *IDMap
	msgs  *msgRegistry
	// self 返回机器人自己的内部 id（IDMap 命名空间）。
	self func() int64
	// isSelf 判断某个 openid 是不是机器人自己（多群时每个群一个 member_openid）。
	isSelf func(string) bool
}

// atTarget 把一个被 @ 的 openid 翻成 at 段的 target。
//
// 关键：被 @ 的是**机器人自己**时必须用 SelfID，不能现走 IDMap 分一个新号 ——
// lsp/parser/parser.go:99-103 判断「是否 @ 了我」用的是 AtTarget == GetUin()，
// 而 GetUin 就是上层缓存的 SelfID。早先这里给自身 openid 现分配了一个小整数，
// 与 SelfID（当时是官方 user.id）永不相等 → 实测「@ 了反而不理，没 @ 反而管用」。
func (p *eventParser) atTarget(openid string) int64 {
	if openid == "" {
		return 0
	}
	if p.isSelf != nil && p.isSelf(openid) {
		if p.self != nil {
			if id := p.self(); id > 0 {
				return id
			}
		}
	}
	return p.userID(openid)
}

// userID 把一个 openid 映射成内部 int64（空的 openid 得到 0）。
func (p *eventParser) userID(openid string) int64 {
	if openid == "" || p.ids == nil {
		return 0
	}
	return p.ids.ToInt64(p.appID, openid)
}

// segments 把事件体翻成 DDBOT 的 []adapter.MessageSegment。
//
// 顺序：引用段 → 正文（<@openid> 拆成 at 段）→ 附件。
// 段类型名沿用 adapter.ConvertToMessageElements 认识的那几种（text/at/image/record/file/video/reply）。
func (p *eventParser) segments(payload *wsMessagePayload) []adapter.MessageSegment {
	out := make([]adapter.MessageSegment, 0, 4)

	// 被引用的消息：给出内部 message id，让上层能识别「这条在回复谁」。
	if ref := payload.quotedRefIdx(); ref != "" && p.msgs != nil {
		if rec, ok := p.msgs.byRefIdx(ref); ok {
			out = append(out, adapter.MessageSegment{
				Type: "reply",
				Data: map[string]interface{}{"id": float64(rec.ID)},
			})
		}
	}

	out = append(out, p.textSegments(payload.text())...)

	for i, att := range payload.Attachments {
		// 平台的附件字段形态不完全固定（content_type 偶尔缺失），打一条调试日志方便对账；
		// URL 去掉 query（rkey 是临时鉴权串，不进日志）。
		logger.Debugf("%s: 附件[%d] content_type=%q filename=%q size=%d url=%s",
			ConfigKeyPrefix, i, att.ContentType, att.FileName, att.Size, clipAttachmentURL(att.URL))
		out = append(out, attachmentSegment(att))
	}
	return out
}

// textSegments 把正文里的 <@openid> 协议标记拆成 at 段，其余按 text 段保留。
func (p *eventParser) textSegments(content string) []adapter.MessageSegment {
	var out []adapter.MessageSegment
	appendText := func(s string) {
		if s == "" {
			return
		}
		out = append(out, adapter.MessageSegment{
			Type: "text",
			Data: map[string]interface{}{"text": s},
		})
	}

	for {
		i := strings.Index(content, "<@")
		if i < 0 {
			break
		}
		j := strings.Index(content[i:], ">")
		if j < 0 {
			break
		}
		appendText(content[:i])
		openid := content[i+2 : i+j]
		if openid != "" {
			out = append(out, adapter.MessageSegment{
				Type: "at",
				Data: map[string]interface{}{
					"qq":     float64(p.atTarget(openid)),
					"openid": openid,
				},
			})
		}
		content = content[i+j+1:]
	}
	appendText(content)
	return out
}

// attachmentSegment 把一个附件翻译成消息段。
//
// 图片判定是 **content_type 优先、文件名后缀兜底**：只认 content_type 会让「平台没给
// content_type」的图直接退化成文件段 —— 实测踩过：用户发的图解析成 file，/倒放 报
// 「参数错误 - 未找到图片」。图片段的 url 是**临时鉴权下载链接**
// （multimedia.nt.qq.com.cn/download?appid=…&fileid=…&rkey=…&spec=0），
// 所以 file 也一并填 URL —— DDBOT 自己的约定就是「远程图片把 URL 放 File」
// （lsp/mmsg/image.go:130），而取图函数 messageImageURL 是「Url 优先、File 兜底」。
//
// 非图片：
//   - 语音 → record（adapter.ConvertToMessageElements 有 record 分支 → VoiceSegment）；
//   - 视频 → **file**：ConvertToMessageElements 没有 video 分支，映射成 video 会被静默丢掉，
//     宁可当文件保住 URL 和文件名；
//   - 其它 → file。
//
// 认不出来的绝不丢事件：一律兜到 file 段，原始字段另存在 name 里备查。
func attachmentSegment(att wsAttachment) adapter.MessageSegment {
	ct := strings.ToLower(strings.TrimSpace(att.ContentType))
	name := att.FileName
	switch {
	case isImageContentType(ct), isImageFileName(name):
		return adapter.MessageSegment{Type: "image", Data: map[string]interface{}{
			"url": att.URL, "file": att.URL, "name": name,
		}}
	case strings.HasPrefix(ct, "audio/"), ct == "voice", ct == "silk", ct == "amr":
		return adapter.MessageSegment{Type: "record", Data: map[string]interface{}{
			"url": att.URL, "name": name,
		}}
	default:
		return adapter.MessageSegment{Type: "file", Data: map[string]interface{}{
			"url": att.URL, "name": name,
		}}
	}
}

// isImageContentType 判断附件的 content_type 是不是图片（实测见过 image/jpeg、image/png、image/gif）。
func isImageContentType(ct string) bool {
	return strings.HasPrefix(ct, "image/") || ct == "image"
}

// isImageFileName 用文件名后缀兜底（平台偶尔不给 content_type）。
func isImageFileName(name string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".heic", ".avif":
		return true
	}
	return false
}

// clipAttachmentURL 只保留 scheme+host+path：query 里的 rkey 是**临时鉴权串**，不许进日志。
func clipAttachmentURL(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + u.Path
	}
	return "<unparsable>"
}

// trimAtPrefix 去掉群 @ 事件 content 的前导空白。
//
// 实测：机器人的 @ 前缀被平台去掉后**残留一个空格**（content 是 " hi"），
// 不处理会让命令解析和模板渲染都多一个前导空格。
func trimAtPrefix(content string) string {
	return strings.TrimLeft(content, " 	")
}

// readyPayload 是 READY 事件体（实测：{"version":1,"session_id":"…","user":{"id":"1234567890123456789",…},"shard":[0,1]}）。
//
// user.id 是机器人的**官方身份 id**（实测是一串十进制数字，如 1234567890123456789），
// 既不是 openid，也**绝不能**当 SelfID 用 —— GetSelfID 绝不采信这里的值。
//
// 为什么（这就是「@ 了机器人反而不理」的根因，已修，别走回头路）：
//   - SelfID 必须与事件里 at 段的 target 同源。at 段的 target 走 IDMap 命名空间
//     （小整数），而这里的 user.id 是官方大整数；两份混用，lsp/parser 的
//     「AtTarget == utils.GetBot().GetUin()」等式恒假 → @ 了反而不理、没 @ 反而执行。
//   - 正确来源是 GET /v2/groups/{openid}/bot_state 的 member_openid（再经 IDMap 推导），
//     见 adapter.go 的 resolveSelfFromGroup / setSelfOpenID。
//
// 所以这里的 user.id 只用于**日志记录**（adapter.go handleReady 里那两条 Info），不做任何判定。
type readyPayload struct {
	SessionID string `json:"session_id"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"user"`
	Shard []int `json:"shard"`
}

// groupOpPayload 是 GROUP_ADD_ROBOT / GROUP_DEL_ROBOT
// （实测：{"group_openid":"…","op_member_openid":"…","timestamp":1794385951}，timestamp 是 unix 秒）。
type groupOpPayload struct {
	GroupOpenID    string `json:"group_openid"`
	GroupID        string `json:"group_id"`
	OpMemberOpenID string `json:"op_member_openid"`
	Timestamp      int64  `json:"timestamp"`
}

func (p *groupOpPayload) groupOpenid() string { return firstNonEmpty(p.GroupOpenID, p.GroupID) }

func (p *groupOpPayload) timeUnix() int64 {
	if p.Timestamp > 0 {
		return p.Timestamp
	}
	return time.Now().Unix()
}

// memberOpPayload 是 GROUP_MEMBER_ADD / GROUP_MEMBER_REMOVE。
type memberOpPayload struct {
	GroupOpenID    string `json:"group_openid"`
	GroupID        string `json:"group_id"`
	MemberOpenID   string `json:"member_openid"`
	OpMemberOpenID string `json:"op_member_openid"`
	Timestamp      int64  `json:"timestamp"`
}

func (p *memberOpPayload) groupOpenid() string { return firstNonEmpty(p.GroupOpenID, p.GroupID) }

// interactionPayload 是 INTERACTION_CREATE。
//
// DDBOT 没有「按钮交互」这类事件，这里映射成 NoticeEvent（NoticeType = "interaction"）：
// 不丢事件、不 panic，上层当前不会处理它；要真用起来得等 lsp 侧加事件类型。
type interactionPayload struct {
	ID          string `json:"id"`
	Type        int    `json:"type"`
	GroupOpenID string `json:"group_openid"`
	GroupID     string `json:"group_id"`
	UserOpenID  string `json:"user_openid"`
	Timestamp   int64  `json:"timestamp"`
	Data        struct {
		Type     int `json:"type"`
		Resolved struct {
			ButtonData string `json:"button_data"`
			ButtonID   string `json:"button_id"`
			UserID     string `json:"user_id"`
		} `json:"resolved"`
	} `json:"data"`
}

// friendOpPayload 是 FRIEND_ADD / FRIEND_DEL。
type friendOpPayload struct {
	OpenID    string `json:"openid"`
	Timestamp int64  `json:"timestamp"`
}

// joinRequestPayload 是 GROUP_JOIN_REQUEST。
type joinRequestPayload struct {
	GroupOpenID    string `json:"group_openid"`
	GroupID        string `json:"group_id"`
	JoinRequestID  string `json:"join_request_id"`
	MemberOpenID   string `json:"member_openid"`
	Username       string `json:"username"`
	ApplyAt        int64  `json:"apply_at"`
	OpMemberOpenID string `json:"op_member_openid"`
}

// auditPayload 是 MESSAGE_AUDIT_PASS / MESSAGE_AUDIT_REJECT。
type auditPayload struct {
	MessageAudit struct {
		AuditID     string `json:"audit_id"`
		MessageID   string `json:"message_id"`
		GroupOpenID string `json:"group_openid"`
		UserOpenID  string `json:"user_openid"`
		AuditTime   string `json:"audit_time"`
		CreateTime  string `json:"create_time"`
	} `json:"message_audit"`
}

// unmarshalEvent 解一个事件体；坏 JSON 只记日志不 panic（网关那边已经 recover 了一层，
// 但这里主动收口，免得半截数据流进业务）。
func unmarshalEvent(eventType string, data json.RawMessage, out interface{}) bool {
	if len(data) == 0 {
		logger.Warnf("%s: 事件 %s 的 d 字段为空，忽略", ConfigKeyPrefix, eventType)
		return false
	}
	if err := json.Unmarshal(data, out); err != nil {
		logger.Errorf("%s: 解析事件 %s 失败：%v", ConfigKeyPrefix, eventType, err)
		return false
	}
	return true
}
