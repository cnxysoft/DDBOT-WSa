package cfg

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ConfigFormatIssue 配置文件里疑似格式问题的一处
type ConfigFormatIssue struct {
	Line    int    // 行号，1 起
	Kind    string // fullwidth_colon / duplicate_key / tab_indent
	Message string
	Text    string // 原始行（过长时截断）
}

// CheckConfigFormat 按行扫一遍配置文本，找出「看起来写了、其实读不到」的格式问题。
//
// 只做高置信度判断：宁可漏报也不误报 —— 启动日志里刷一堆假警报比不报还糟。
// 起因有两个：群友配置里 签到："" 用了全角冒号，整块退化成多行字符串、该键静默失效；
// 以及写回把 sessdata 改写成 SESSDATA 之后，同一份配置里出现两个等价键，
// 而 viper 大小写不敏感，只有其中一个生效。
//
// 块标量与多行纯量里的内容会原样跳过：那里面「键: 值」长相的行只是文本，
// 拿它们判重复键或全角冒号全是误报。
func CheckConfigFormat(content string) []ConfigFormatIssue {
	var issues []ConfigFormatIssue
	seen := make(map[int]map[string]int) // 缩进层级 -> 小写键名 -> 首次出现行号
	seqIndent := -1                      // 当前所处的序列缩进；-1 表示不在序列里
	blockIndent := -1                    // 正在块标量（| / >）里，记下所属键的缩进
	plainIndent := -1                    // 正在多行纯量里
	lastKeyLevel := -1                   // 上一个键的缩进
	lastKeyValEmpty := true              // 上一个键的值是不是空的

	for i, raw := range strings.Split(content, "\n") {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		level := len(indent)

		// 块标量：更深（或空）的行都是它的内容
		if blockIndent >= 0 {
			if trimmed == "" || level > blockIndent {
				continue
			}
			blockIndent = -1
		}
		// 多行纯量的续行同理
		if plainIndent >= 0 {
			if trimmed == "" || level > plainIndent {
				continue
			}
			plainIndent = -1
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// YAML 不允许用 Tab 做缩进
		if strings.Contains(indent, "\t") {
			issues = append(issues, newFormatIssue(lineNo, "tab_indent",
				"缩进里用了 Tab，YAML 只允许空格", line))
			continue
		}

		// 序列项本身不是键；它下面更深的那几行属于「每一项各写一遍」的映射，
		// 不参与同层重复键判断，否则每项都有的 name/value 会互相误报
		if trimmed == "-" || strings.HasPrefix(trimmed, "- ") {
			seqIndent = level
			continue
		}
		if seqIndent >= 0 && level > seqIndent {
			continue
		}
		seqIndent = -1

		key, rest, ok := splitKeyLine(trimmed)
		if !ok {
			// 上一行是个没有值的键、而这一行更深 —— 既可能是子键，也可能是那个键的
			// 多行纯量续行。歧义位置上只认「右边像值」的写法，宁漏不误。
			ambiguous := lastKeyLevel >= 0 && level > lastKeyLevel && lastKeyValEmpty
			if looksLikeFullwidthColonKey(trimmed, !ambiguous) {
				issues = append(issues, newFormatIssue(lineNo, "fullwidth_colon",
					"键后面是全角冒号「：」，YAML 不认；这一行连同它下面的同级键会被当成一段字符串，配置静默失效", line))
			}
			continue
		}

		// 记下这个键的值形态：块标量和多行纯量都会让后面的行变成「文本」
		val := strings.TrimSpace(rest)
		lastKeyLevel = level
		lastKeyValEmpty = val == ""
		switch {
		case strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">"):
			blockIndent = level
		case val != "" && !strings.HasPrefix(val, "\"") && !strings.HasPrefix(val, "'") &&
			!strings.HasPrefix(val, "[") && !strings.HasPrefix(val, "{"):
			plainIndent = level
		}

		// 出现更浅的键，说明更深的那几层映射已经结束
		for deeper := range seen {
			if deeper > level {
				delete(seen, deeper)
			}
		}
		if seen[level] == nil {
			seen[level] = make(map[string]int)
		}
		folded := strings.ToLower(key)
		if first, dup := seen[level][folded]; dup {
			issues = append(issues, newFormatIssue(lineNo, "duplicate_key",
				fmt.Sprintf("和第 %d 行的 %q 是同一个键（viper 大小写不敏感，只有其中一个生效）", first, key), line))
		} else {
			seen[level][folded] = lineNo
		}
	}
	return issues
}

// CheckConfigFormatFile 读配置文件校对格式，把问题写进日志，并返回发现的问题。
//
// 只警告不中断：这些都是「看起来写了、其实读不到」的问题，改不改由用户决定。
// 真正让程序起不来的语法错误，viper 在 ReadInConfig 阶段就会报出来。
func CheckConfigFormatFile(path string) []ConfigFormatIssue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	issues := CheckConfigFormat(string(data))
	if len(issues) == 0 {
		return nil
	}
	logger.Warnf("配置文件 %s 有 %d 处格式问题，涉及的键可能读不到：", path, len(issues))
	for _, is := range issues {
		logger.Warnf("  第 %d 行 [%s] %s —— %s", is.Line, is.Kind, is.Message, is.Text)
	}
	return issues
}

func newFormatIssue(line int, kind, message, text string) ConfigFormatIssue {
	if len(text) > 80 {
		text = text[:80] + "..."
	}
	return ConfigFormatIssue{Line: line, Kind: kind, Message: message, Text: text}
}

// splitKeyLine 从一行里切出键名和冒号后面的部分；不是键行时返回 false。
// 只认「第一个冒号后面跟空格或行尾」这种 YAML 映射写法，
// 所以 url: https://x 切出的是 url，而一段中文说明直接不认。
func splitKeyLine(trimmed string) (key, rest string, ok bool) {
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", "", false
	}
	if idx+1 < len(trimmed) && trimmed[idx+1] != ' ' && trimmed[idx+1] != '\t' {
		return "", "", false
	}
	key = strings.Trim(strings.TrimSpace(trimmed[:idx]), "\"'")
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	return key, trimmed[idx+1:], true
}

// looksLikeFullwidthColonKey 判断这一行是不是「键 + 全角冒号 + 值」。
// 左边那段要像个键名（不含空白、不太长）。
// allowBare 为 true 时右边只要非空就算 —— 那种位置不会是纯量续行，
// 所以 SESSDATA：abc123 和 签到："" 都要报；为 false（歧义位置）时只认像值的右边。
func looksLikeFullwidthColonKey(trimmed string, allowBare bool) bool {
	idx := strings.Index(trimmed, "：")
	if idx <= 0 {
		return false
	}
	if strings.Contains(trimmed, ":") {
		// 已经出现过半角冒号，说明全角冒号在值或注释里
		return false
	}
	left := strings.TrimSpace(trimmed[:idx])
	if left == "" || strings.ContainsAny(left, " \t") || len([]rune(left)) > 40 {
		return false
	}
	right := strings.TrimSpace(trimmed[idx+len("："):])
	return right == "" || allowBare || looksLikeYAMLValue(right)
}

// bareValueRe 一串不含空白与中文的记号：cookie、路径、数字、布尔都长这样。
var bareValueRe = regexp.MustCompile("^[A-Za-z0-9_./%+=:@~^-]+$")

func looksLikeYAMLValue(s string) bool {
	switch s[0] {
	case '"', '\'', '[', '{', '&', '*', '!', '|', '>', '~':
		return true
	}
	if (s[0] >= '0' && s[0] <= '9') || s[0] == '-' || s[0] == '+' {
		return true
	}
	if bareValueRe.MatchString(s) {
		return true
	}
	low := strings.ToLower(s)
	for _, w := range []string{"true", "false", "null", "yes", "no", "on", "off"} {
		if low == w || strings.HasPrefix(low, w+" ") {
			return true
		}
	}
	return false
}
