package cfg

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/Sora233/MiraiGo-Template/config"
)

var cfgMu sync.Mutex

// WriteConfigKeyValues 批量更新 application.yaml 中的键值。
// key 使用点号分层（如 "weibo.alertGroupId"），写入值会做 YAML 双引号转义；
// 除目标键外其它内容与行内注释保持不变。
//
// 成品证据：lsp/cfg.WriteConfigKeyValues @yamlutil.go:26（0xae8de0，736B）。
// 调用方：lsp/bilibili.persistRefreshedBilibiliCookie(cookie_refresh.go:391)、
// persistLiveWebsocketIdentity(live_websocket.go:1143)、
// runBilibiliQRLoginOnce(manual_qr.go:19)；参数为 map[string]string
// （调用点 0xd14895 处 lea 栈上 map 后 call）。
func WriteConfigKeyValues(kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()

	cfgFile := config.GlobalConfig.ConfigFileUsed()
	if cfgFile == "" {
		cfgFile = "application.yaml"
	}
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return err
	}
	fi, err := os.Stat(cfgFile)
	if err != nil {
		return err
	}
	content, err := updateConfigContent(string(data), kv)
	if err != nil {
		return err
	}
	return os.WriteFile(cfgFile, []byte(content), fi.Mode().Perm())
}

// updateConfigContent 逐个更新 content 中的键，键按字典序排序后串行写入，
// 并保留原始换行风格（LF/CRLF）、UTF-8 BOM 与结尾换行。
//
// 成品证据：lsp/cfg.updateConfigContent @yamlutil.go:54（0xae9100，1248B）。
// 字符串证据：CRLF（0x10982c6）、LF（0x11212c8）、BOM（0x1098989）。
func updateConfigContent(content string, kv map[string]string) (string, error) {
	nl := "\n"
	if strings.Index(content, "\r\n") >= 0 {
		nl = "\r\n"
	}
	hasTrailingNewline := len(content) > 0 && content[len(content)-1] == '\n'

	bom := ""
	if strings.HasPrefix(content, "\uFEFF") {
		content = content[3:]
		bom = "\uFEFF"
	}
	// 统一成 LF 处理，最后再按原始风格还原。
	content = strings.ReplaceAll(content, "\r\n", "\n")

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var err error
		content, err = updateConfigContentValue(content, k, kv[k])
		if err != nil {
			return "", err
		}
	}
	if hasTrailingNewline && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if nl == "\r\n" {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	return bom + content, nil
}

// updateConfigContentValue 更新单个 "section.name" 键：
//   - 找到 section 头所在行，扫描出该 section 的子行范围与最小缩进；
//   - 若已存在同名键（缩进匹配）则整行替换，并保留原行内注释；
//   - 否则在 section 末尾（跳过尾部空行）按目标缩进插入新行；
//   - 整个文件中找不到 section 时，在文末追加 "section:" 与两空格缩进的键行。
//
// 成品证据：lsp/cfg.updateConfigContentValue @yamlutil.go:91（0xae95e0，3360B）。
// 正则字符串证据："^(\\s*)"（0x109faa2）+ QuoteMeta(section) + "\\s*:\\s*(?:#.*)?$"（0x10afcd5）；
// 键行正则："^(\\s*)" + QuoteMeta(name) + "\\s*:"（0x1099833）。
// 错误串证据："invalid config key format: %s (expected section.name)"（0x10e008e，53B）。
func updateConfigContentValue(content string, key string, value string) (string, error) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid config key format: %s (expected section.name)", key)
	}
	section, keyName := parts[0], parts[1]

	lines := strings.Split(content, "\n")

	// section 头：整行（允许行尾注释）只有 "section:"，捕获其前导缩进。
	sectionRe := regexp.MustCompile("^(\\s*)" + regexp.QuoteMeta(section) + "\\s*:\\s*(?:#.*)?$")
	matchLine := -1
	indentLen := 0
	for i, line := range lines {
		m := sectionRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indentLen = len(m[1]) - len(strings.TrimLeft(m[1], " \t"))
		matchLine = i
		break
	}

	quoted := strconv.Quote(value)

	if matchLine >= 0 {
		// 1) 扫描 section 范围：遇到缩进 <= section 缩进的有效行即结束，
		//    同时记录子行最小缩进（用于新键的对齐）。
		end := len(lines)
		targetIndent := -1
		for i := matchLine + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			thisIndent := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t"))
			if indentLen >= thisIndent {
				end = i
				break
			}
			if targetIndent < 0 || thisIndent < targetIndent {
				targetIndent = thisIndent
			}
		}
		if targetIndent < 0 {
			targetIndent = indentLen + 2
		}

		// 2) 在 section 范围内查找缩进匹配的同名键。
		keyRe := regexp.MustCompile("^(\\s*)" + regexp.QuoteMeta(keyName) + "\\s*:")
		foundIdx := -1
		for i := matchLine + 1; i < end; i++ {
			m := keyRe.FindStringSubmatch(lines[i])
			if m == nil || len(m) < 2 {
				continue
			}
			keyIndent := len(m[1]) - len(strings.TrimLeft(m[1], " \t"))
			if keyIndent == targetIndent {
				foundIdx = i
				break
			}
		}

		if foundIdx >= 0 {
			line := lines[foundIdx]
			colonIdx := strings.Index(line, ":")
			prefix := line[:colonIdx+1]
			comment := yamlInlineComment(line[colonIdx+1:])
			newLine := prefix + " " + quoted
			if comment != "" {
				newLine += " " + comment
			}
			lines[foundIdx] = newLine
			return strings.Join(lines, "\n"), nil
		}

		// 3) 未找到键：插到 section 末尾，跳过尾部空行。
		for end > matchLine+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		newLine := strings.Repeat(" ", targetIndent) + keyName + ": " + quoted
		lines = append(lines[:end], append([]string{newLine}, lines[end:]...)...)
		return strings.Join(lines, "\n"), nil
	}

	// 4) 整个文件没有该 section：文末追加新的 section 与键行。
	hadTrailingEmpty := len(lines) > 0 && lines[len(lines)-1] == ""
	if hadTrailingEmpty {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, section+":")
	lines = append(lines, "  "+keyName+": "+quoted)
	if hadTrailingEmpty {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), nil
}

// yamlInlineComment 提取行内注释：从第一个未被引号包裹、且位于行首或空白之后的
// '#' 开始，返回其 TrimSpace 结果；没有注释时返回空串。
//
// 成品证据：lsp/cfg.yamlInlineComment @yamlutil.go:176（0xaea300，448B）。
// 双引号内支持反斜杠转义；单引号与双引号互不干扰。
func yamlInlineComment(s string) string {
	inDouble := false
	inSingle := false
	escaped := false
	prevSpace := true
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		next := i + size
		if !escaped {
			switch r {
			case '\\':
				if inDouble {
					escaped = true
					prevSpace = false
					i = next
					continue
				}
			case '"':
				if !inSingle {
					inDouble = !inDouble
				}
			case '#':
				if !inSingle && !inDouble && (i == 0 || prevSpace) {
					return strings.TrimSpace(s[i:])
				}
			case '\'':
				if !inDouble {
					inSingle = !inSingle
				}
			}
		}
		escaped = false
		prevSpace = unicode.IsSpace(r)
		i = next
	}
	return ""
}
