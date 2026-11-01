package admin

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
)

// 模板编辑：默认模板内嵌只读；自定义模板落盘到 ./template/ 目录（.tmpl），
// loader 的 fsnotify 监听会自动热重载——写入即生效，无需重启。

var templateNamePattern = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// templateDirPath 自定义模板目录（与 loader.go 的 templateDir 常量一致）。
const templateDirPath = "template"

// safeTemplateName 校验模板名：小写字母数字下划线+至少一个点（分组.名称），
// 拒绝路径穿越（/ .. 等）。
func safeTemplateName(name string) (string, bool) {
	if !templateNamePattern.MatchString(name) || len(name) > 100 {
		return "", false
	}
	if strings.Contains(name, "..") {
		return "", false
	}
	return name + ".tmpl", true
}

type TemplateItem struct {
	Name       string `json:"name"`
	Customized bool   `json:"customized"`
	Category   string `json:"category"` // notify / command / trigger 等
}

func templateCategory(name string) string {
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return "other"
}

// customTemplateFiles 列出自定义目录里已有的 .tmpl 文件名（不含后缀）。
func customTemplateFiles() (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(templateDirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tmpl") {
			out[strings.TrimSuffix(e.Name(), ".tmpl")] = true
		}
	}
	return out, nil
}

// handleTemplates GET /api/v1/templates
// 返回全部模板（默认 + 自定义覆盖标记）。
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	custom, err := customTemplateFiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取自定义模板目录失败")
		return
	}
	items := make([]TemplateItem, 0)
	seen := map[string]bool{}
	for _, name := range template.DefaultTemplateNames() {
		seen[name] = true
		items = append(items, TemplateItem{
			Name:       name,
			Customized: custom[name],
			Category:   templateCategory(name),
		})
	}
	// 自定义目录里存在但不在默认清单中的（新增模板，如 trigger 私有扩展）
	for name := range custom {
		if !seen[name] {
			items = append(items, TemplateItem{Name: name, Customized: true, Category: templateCategory(name)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": items})
}

// handleTemplateDetail GET /api/v1/templates/detail?name=xxx
// 返回单个模板：当前生效内容 + 默认内容。
func (s *Server) handleTemplateDetail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	fileName, ok := safeTemplateName(name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法模板名")
		return
	}
	def, err := template.DefaultTemplateContent(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "模板不存在")
		return
	}
	content := def
	customized := false
	if b, err := os.ReadFile(filepath.Join(templateDirPath, fileName)); err == nil {
		content = string(b)
		customized = true
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":       name,
		"content":    content,
		"default":    def,
		"customized": customized,
	})
}

// handleTemplateSave PUT /api/v1/templates
// body: {"name": "...", "content": "..."}——语法校验通过后写入自定义目录，热生效。
func (s *Server) handleTemplateSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	fileName, ok := safeTemplateName(req.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法模板名")
		return
	}
	if _, err := template.DefaultTemplateContent(req.Name); err != nil {
		writeErr(w, http.StatusNotFound, "模板不存在")
		return
	}
	// 语法校验（写入前防坏模板导致推送崩坏）
	if err := template.ValidateTemplate(req.Content); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("模板语法错误：%v", err))
		return
	}
	if err := os.MkdirAll(templateDirPath, 0766); err != nil {
		writeErr(w, http.StatusInternalServerError, "创建模板目录失败")
		return
	}
	if err := writeFileAtomic(filepath.Join(templateDirPath, fileName), []byte(req.Content), 0666); err != nil {
		writeErr(w, http.StatusInternalServerError, "写入失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "已保存并热生效"})
}

// handleTemplateReset DELETE /api/v1/templates?name=xxx
// 删除自定义文件，恢复默认模板。
func (s *Server) handleTemplateReset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	fileName, ok := safeTemplateName(name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法模板名")
		return
	}
	path := filepath.Join(templateDirPath, fileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeErr(w, http.StatusNotFound, "该模板未自定义")
		return
	}
	if err := os.Remove(path); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "已恢复默认模板"})
}

// ===== 实时预览 =====

// previewVarPathRe 提取模板中的变量路径（{{ .a.b.C }} → a.b.C，函数参数内的字段同样会捕获）
var previewVarPathRe = regexp.MustCompile(`\.([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)*)`)

// previewAliasDefRe 识别 {{ $x := .path }} 形式的别名定义（如 twitter 模板 $dynamic := .dynamic）
var previewAliasDefRe = regexp.MustCompile(`\$(\w+) := \.([a-zA-Z_][a-zA-Z0-9_.]*)`)

// previewAliasUseRe 识别 $x.field 形式的别名使用，映射回真实路径
var previewAliasUseRe = regexp.MustCompile(`\$(\w+)\.([a-zA-Z_][a-zA-Z0-9_.]*)`)

// previewPathValues 完整路径优先的类型特判（同名字段在不同结构里类型不同，如 dynamic.Type 是数字）
var previewPathValues = map[string]interface{}{
	"dynamic.Type": int64(8), // bilibili 动态类型 8=投稿视频，走主分支
}

// previewTypedValues 按变量末段名给特殊类型的示例值（bool/时间/数字），其余见 previewStringValues
var previewTypedValues = map[string]interface{}{
	"living":            true,
	"success":           true,
	"ShouldCompact":     false,
	"HasVideo":          true,
	"is_video":          true,
	"is_shorts":         false,
	"WithOrigin":        false,
	"WithRetweet":       false,
	"WithQuote":         false,
	"compact_miss":      false,
	"live_time":         time.Date(2026, 9, 28, 20, 0, 0, 0, time.Local),
	"publish_timestamp": time.Now().Add(-2 * time.Hour).Unix(),
	"timestamp":         time.Now().Add(-2 * time.Hour).Unix(),
	"duration_seconds":  int64(180),
	"score":             int64(42),
	"Id":                int64(10001),
	"Uin":               int64(10001),
	"id":                int64(10001),
	"uin":               int64(10001),
	"User_id":           int64(10001),
	"user_id":           int64(10001),
	"UserID":            int64(10001),
	"group_code":        int64(65536),
	"GroupCode":         int64(65536),
	"Target":            int64(65536),
	"target":            int64(65536),
	"viewer_count":      int64(12345),
	// 投票结构：线上是指针（nil=无投票）；map 会让 eq $vote nil 不可比，一律给 nil
	"Vote": nil,
}

// previewSliceValues range 遍历类变量（图片列表等）给 slice 示例
var previewSliceValues = map[string]interface{}{
	"Images":    []string{"https://example.com/img1.jpg", "https://example.com/img2.jpg"},
	"pictures":  []string{"https://example.com/pic1.jpg", "https://example.com/pic2.jpg"},
	"Pics":      []string{"https://example.com/pic1.jpg"},
	"ImageUrls": []string{"https://example.com/img1.jpg", "https://example.com/img2.jpg"},
	// DescViewPictures 的元素是带 Pics 字段的结构
	"DescViewPictures": []map[string]interface{}{{"Pics": []string{"https://example.com/pic1.jpg", "https://example.com/pic2.jpg"}}},
	// acfun 的 Imgs 元素带 Url 字段
	"Imgs": []map[string]interface{}{{"Url": "https://example.com/img1.jpg"}},
	// bilibili 附加内容（Type 1=商品 6=预约），twitter 媒体列表
	"Addons": []map[string]interface{}{{"Type": int64(6), "Reserve": map[string]interface{}{"Title": "直播预约", "Desc": "预约描述示例", "Lottery": ""}}},
	"Media":  []map[string]interface{}{{"Type": "image", "Url": "https://example.com/media1.jpg", "Base64": ""}},
}

// previewStringValues 常用变量中文示例值（大小写变体分开收录）
var previewStringValues = map[string]string{
	"name": "星野源", "Name": "星野源", "Nickname": "星野源", "nickname": "星野源",
	"UserName": "星野源", "title": "【歌回】深夜弹唱", "Title": "【歌回】深夜弹唱",
	"url": "https://live.bilibili.com/2233", "Url": "https://t.bilibili.com/123456",
	"cover": "https://example.com/cover.jpg", "CoverUrl": "https://example.com/cover.jpg",
	"parent_area_name": "网络游戏", "area_name": "英雄联盟",
	"content": "新专辑上线啦，今晚聊聊创作过程", "Content": "新专辑上线啦，今晚聊聊创作过程",
	"Desc": "个人简介", "desc": "个人简介", "Date": "2026-09-28 20:00:00", "date": "2026-09-28 20:00:00",
	"User": "星野源", "user": "星野源", "Type": "直播", "type": "直播",
	"Src": "bilibili", "src": "bilibili", "Text": "动态正文示例",
	"Action": "发布了动态", "action": "发布了动态",
	"Detail": "详情示例", "Base": "基础信息",
	"game_name": "最终幻想14", "thumbnail": "https://example.com/thumb.jpg", "login": "hoshino_gen",
	"list_site": "bilibili", "group_name": "示例群", "help_cmd": "help",
	"GroupName": "示例群", "groupName": "示例群",
	"TopicName": "日常", "OriginTopicName": "直播间日常", "OriginTitle": "原动态标题",
	"DynamicUrl": "https://t.bilibili.com/123456", "OriginDyUrl": "https://t.bilibili.com/654321",
	"Description": "动态正文示例", "ShareURL": "https://example.com/share", "VideoURL": "https://example.com/video",
	"VideoThumb": "https://example.com/vthumb.jpg",
	"VideoId":    "v12345678", "Caption": "视频标题示例", "ReplaceUbbText": "动态内容示例", "Summary": "图文简介示例",
	"HeadText": "相关推荐", "adMark": "广告", "Badge": "标签", "Intro": "简介示例", "Author": "作者名",
	"Tips": "提示文案", "DescText": "描述文本", "DescPlayUrl": "https://example.com/play", "PlayUrl": "https://example.com/play",
	"Lottery": "抽奖信息", "Index": "选项 A", "TranslatedText": "（翻译）推文正文示例", "Media": "媒体内容",
	"OnlineUsers": "1.2万", "PublishTime": "2026-09-27 20:00:00", "Content1": "微博正文补充",
	"Desc1": "补充描述一", "Desc2": "补充描述二", "Desc3": "补充描述三", "parse_post": "解析结果",
	"WatchCommand": "watch", "UnwatchCommand": "unwatch", "ListCommand": "list",
	"HelpCommand": "help", "ConfigCommand": "config", "SilenceCommand": "silence",
	"FreshCommand": "fresh", "NewsCommand": "watch -t news", "CheckinCommand": "checkin",
}

// previewContextPaths 这些路径的值必须是真实的消息上下文对象（reply/outputIList 等函数依赖）
var previewContextPaths = map[string]bool{"msg": true, "message_context": true}

func previewGroupMessage() *adapter.GroupMessage {
	return &adapter.GroupMessage{
		ID:        10001,
		GroupCode: 65536,
		GroupName: "示例群",
		Time:      time.Now().Unix(),
		Sender:    &adapter.SenderInfo{UserID: 10001, Uin: 10001, Nickname: "群小明", Card: "群小明", Role: "member"},
	}
}

// previewAssign 沿路径把值塞进嵌套 map。
// 占位串（"示例值"）不覆盖已有值——避免单段占位把先建好的嵌套 map 冲掉。
func previewAssign(root map[string]interface{}, path []string, leaf interface{}) {
	cur := root
	for _, seg := range path[:len(path)-1] {
		next, ok := cur[seg].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cur[seg] = next
		}
		cur = next
	}
	last := path[len(path)-1]
	if s, isStr := leaf.(string); isStr && s == "示例值" {
		if _, exists := cur[last]; exists {
			return
		}
	}
	cur[last] = leaf
}

// previewAssignForce 沿路径赋值，末段强制覆盖（别名重建专用：替换先建的占位串）
func previewAssignForce(root map[string]interface{}, path []string, leaf interface{}) {
	cur := root
	for _, seg := range path[:len(path)-1] {
		next, ok := cur[seg].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cur[seg] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = leaf
}

// previewLeafFor 末段/完整路径 → 示例值（slice > 路径特判 > 特型 > 字符串 > 占位）
func previewLeafFor(full string, last string) interface{} {
	if v, ok := previewSliceValues[last]; ok {
		return v
	}
	if v, ok := previewPathValues[full]; ok {
		return v
	}
	if v, ok := previewTypedValues[last]; ok {
		return v
	}
	if v, ok := previewStringValues[last]; ok {
		return v
	}
	return "示例值"
}

// previewSampleData 根据模板默认内容中出现的变量路径构造嵌套示例数据。
// 支持 {{ $x := .path }} 别名（$x.f → path.f）；未收录变量给占位串，渲染不炸。
// 条件分支类的长尾结构不构造（if 取不到即走 false，预览走主分支更干净）。
func previewSampleData(name string) map[string]interface{} {
	def, err := template.DefaultTemplateContent(name)
	if err != nil {
		def = ""
	}
	root := map[string]interface{}{}

	// ① 别名定义收集：$dynamic := .dynamic → alias[dynamic] = [dynamic]
	// 注意排除 range 形式：range $v := .X 中 $v 是元素变量而非数据别名，
	// 误收会把 .X 从 slice 重建覆盖成 map（bilibili Addons 曾因此炸掉）。
	alias := map[string][]string{}
	for _, loc := range previewAliasDefRe.FindAllStringSubmatchIndex(def, -1) {
		if loc[0] >= 6 && def[loc[0]-6:loc[0]] == "range " {
			continue
		}
		alias[def[loc[2]:loc[3]]] = strings.Split(def[loc[4]:loc[5]], ".")
	}

	// ② 直接路径：.a.b.c（占位串不覆盖已有值）
	for _, m := range previewVarPathRe.FindAllStringSubmatch(def, -1) {
		path := strings.Split(m[1], ".")
		// 消息上下文路径直接给真实对象（reply 等函数依赖）
		if len(path) == 1 && previewContextPaths[path[0]] {
			root[path[0]] = previewGroupMessage()
			continue
		}
		previewAssign(root, path, previewLeafFor(m[1], path[len(path)-1]))
	}

	// ③ 别名重建：$dynamic.X.Y → 按 .dynamic.X.Y 完整路径强制赋值（覆盖 ② 留下的占位串）
	for _, m := range previewAliasUseRe.FindAllStringSubmatch(def, -1) {
		base, ok := alias[m[1]]
		if !ok {
			continue
		}
		path := append(append([]string{}, base...), strings.Split(m[2], ".")...)
		// 前缀若已显式给 nil（如 Vote），不重建——保持 nil 语义（eq $vote nil 需要可比较）
		skip := false
		for i := 1; i <= len(path); i++ {
			if v, ok2 := previewPathValues[strings.Join(path[:i], ".")]; ok2 && v == nil {
				skip = true
				break
			}
			if v, ok2 := previewTypedValues[path[i-1]]; ok2 && v == nil {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		previewAssignForce(root, path, previewLeafFor(strings.Join(path, "."), path[len(path)-1]))
	}
	return root
}

// handleTemplatePreview POST /api/v1/templates/preview
// body: {"name": "...", "content": "..."}——用示例数据渲染，返回渲染文本。
func (s *Server) handleTemplatePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if _, ok := safeTemplateName(req.Name); !ok {
		writeErr(w, http.StatusBadRequest, "非法模板名")
		return
	}
	if _, err := template.DefaultTemplateContent(req.Name); err != nil {
		writeErr(w, http.StatusNotFound, "模板不存在")
		return
	}
	rendered, err := template.RenderPreview(req.Content, previewSampleData(req.Name))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown reply") {
			msg = "该模板使用了 reply/at 等依赖真实消息上下文的函数，静态预览不支持这部分（线上推送不受影响）"
		}
		if strings.Contains(msg, "outputIList") || strings.Contains(msg, "RunIList") {
			msg = "订阅列表需要实时查询订阅数据，静态预览暂不支持（线上推送不受影响）"
		}
		writeErr(w, http.StatusBadRequest, "预览失败："+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rendered": rendered})
}
