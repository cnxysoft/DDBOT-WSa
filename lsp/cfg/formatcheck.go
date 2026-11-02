package cfg

import (
	"fmt"
	"os"
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
func CheckConfigFormat(content string) []ConfigFormatIssue {
	var issues []ConfigFormatIssue
	seen := make(map[int]map[string]int) // 缩进层级 -> 小写键名 -> 首次出现行号
	seqIndent := -1                      // 当前所处的序列缩进；-1 表示不在序列里

	for i, raw := range strings.Split(content, "\n") {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		level := len(indent)

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

		key, ok := splitKeyLine(trimmed)
		if !ok {
			if looksLikeFullwidthColonKey(trimmed) {
				issues = append(issues, newFormatIssue(lineNo, "fullwidth_colon",
					"键后面是全角冒号「：」，YAML 不认；这一行连同它下面的同级键会被当成一段字符串，配置静默失效", line))
			}
			continue
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

// splitKeyLine 从一行里切出键名；不是键行时返回 false。
// 只认「第一个冒号后面跟空格或行尾」这种 YAML 映射写法，
// 所以 url: https://x 切出的是 url，而一段中文说明直接不认。
func splitKeyLine(trimmed string) (string, bool) {
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", false
	}
	if idx+1 < len(trimmed) && trimmed[idx+1] != ' ' && trimmed[idx+1] != '\t' {
		return "", false
	}
	key := strings.Trim(strings.TrimSpace(trimmed[:idx]), "\"'")
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", false
	}
	return key, true
}

// looksLikeFullwidthColonKey 判断这一行是不是「键 + 全角冒号 + 值」。
//
// 刻意保守：左边那段要像个键名（不含空白、不太长），右边那段要像个 YAML 值
// （引号 / 括号 / 数字 / 布尔 / 空）。所以 签到："" 报，而多行纯量里的
// 「注意：这是一段说明」不报 —— 宁可漏报也不误报。
func looksLikeFullwidthColonKey(trimmed string) bool {
	idx := strings.Index(trimmed, "：")
	if idx <= 0 {
		return false
	}
	if strings.Contains(trimmed, ":") {
		// 已经出现过半角冒号，说明全角冒号在值或注释里
		return false
	}
	left := strings.TrimSpace(trimmed[:idx])
	right := strings.TrimSpace(trimmed[idx+len("："):])
	if left == "" || strings.ContainsAny(left, " \t") || len([]rune(left)) > 40 {
		return false
	}
	return right == "" || looksLikeYAMLValue(right)
}

func looksLikeYAMLValue(s string) bool {
	switch s[0] {
	case '"', '\'', '[', '{', '&', '*', '!', '|', '>', '~':
		return true
	}
	if (s[0] >= '0' && s[0] <= '9') || s[0] == '-' || s[0] == '+' {
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
