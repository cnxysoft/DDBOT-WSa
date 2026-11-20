package cfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
)

// 配置补丁：按「从根开始的键路径」改值，只动目标值行，其余内容（注释、空行、
// 缩进、键名大小写、行尾）原样保留。
//
// 为什么不用 yaml.Marshal 整份重写：默认生成的 application.yaml 里有大量
// 说明性注释和空行，整份 marshal 会把它们全部吃掉、把缩进和引号风格也换掉。
// 用户明确要求「直接在默认格式下修改」。本文件就是那个 line-based 实现。

// ConfigPatch 是一次配置修改。
type ConfigPatch struct {
	// Path 是从根开始的键路径，例如 []string{"qqofficial", "app-id"}。
	Path []string
	// Value 是新的值：标量（string/bool/数字/nil）或 map/slice。
	Value interface{}
}

// yamlPatchKeyRe 匹配一行「键: 值」，并保留缩进、引号、冒号两侧空格与行内注释。
// 键里允许中文、点、连字符；以 - 开头的列表项与 # 开头的注释由调用方先排除。
var yamlPatchKeyRe = regexp.MustCompile(`^(\s*)("?)([^"#\n]+?)("?)(\s*):(\s*)(.*)$`)

type yamlPatchLine struct {
	body string
	cr   bool // 该行原本是否以 CRLF 结尾
}

func splitYAMLPatchLines(content string) []yamlPatchLine {
	parts := strings.Split(content, "\n")
	lines := make([]yamlPatchLine, 0, len(parts))
	for _, part := range parts {
		cr := strings.HasSuffix(part, "\r")
		if cr {
			part = strings.TrimSuffix(part, "\r")
		}
		lines = append(lines, yamlPatchLine{body: part, cr: cr})
	}
	return lines
}

func joinYAMLPatchLines(lines []yamlPatchLine) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line.body)
		if line.cr {
			b.WriteByte('\r')
		}
	}
	return b.String()
}

func yamlPatchIndent(body string) int {
	n := 0
	for n < len(body) && (body[n] == ' ' || body[n] == '\t') {
		n++
	}
	return n
}

func yamlPatchBlank(body string) bool   { return strings.TrimSpace(body) == "" }
func yamlPatchComment(body string) bool { return strings.HasPrefix(strings.TrimSpace(body), "#") }

// parseYAMLKeyLine 解析一行「键: 值」，返回正则的捕获组。
func parseYAMLKeyLine(body string) ([]string, bool) {
	trim := strings.TrimSpace(body)
	if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "-") {
		return nil, false
	}
	m := yamlPatchKeyRe.FindStringSubmatch(body)
	if m == nil || strings.TrimSpace(m[3]) == "" {
		return nil, false
	}
	return m, true
}

// yamlPatchChildIndent 找出一个映射块里第一个键的缩进；空块返回 -1。
func yamlPatchChildIndent(lines []yamlPatchLine, parentIndent int) int {
	for _, line := range lines {
		if yamlPatchBlank(line.body) || yamlPatchComment(line.body) {
			continue
		}
		indent := yamlPatchIndent(line.body)
		if indent <= parentIndent {
			continue
		}
		return indent
	}
	return -1
}

// yamlPatchBlockEnd 返回键所在块的结束行（不含）：块内所有缩进更深的行，
// 尾部空行留给下一个键做分隔。
func yamlPatchBlockEnd(lines []yamlPatchLine, keyIdx int) int {
	keyIndent := yamlPatchIndent(lines[keyIdx].body)
	end := keyIdx + 1
	for end < len(lines) {
		body := lines[end].body
		if yamlPatchBlank(body) {
			end++
			continue
		}
		if yamlPatchIndent(body) <= keyIndent {
			break
		}
		end++
	}
	for end > keyIdx+1 && yamlPatchBlank(lines[end-1].body) {
		end--
	}
	return end
}

func isYAMLScalar(v interface{}) bool {
	switch v.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}

// serializeYAMLPatchValue 把值序列化成 YAML 行（不含缩进）。
func serializeYAMLPatchValue(value interface{}) ([]string, error) {
	switch v := value.(type) {
	case nil:
		return []string{"null"}, nil
	case string:
		return []string{quoteYAMLValue(v)}, nil
	case bool:
		return []string{strconv.FormatBool(v)}, nil
	case int:
		return []string{strconv.Itoa(v)}, nil
	case int8:
		return []string{strconv.FormatInt(int64(v), 10)}, nil
	case int16:
		return []string{strconv.FormatInt(int64(v), 10)}, nil
	case int32:
		return []string{strconv.FormatInt(int64(v), 10)}, nil
	case int64:
		return []string{strconv.FormatInt(v, 10)}, nil
	case uint:
		return []string{strconv.FormatUint(uint64(v), 10)}, nil
	case uint8:
		return []string{strconv.FormatUint(uint64(v), 10)}, nil
	case uint16:
		return []string{strconv.FormatUint(uint64(v), 10)}, nil
	case uint32:
		return []string{strconv.FormatUint(uint64(v), 10)}, nil
	case uint64:
		return []string{strconv.FormatUint(v, 10)}, nil
	case float32:
		return []string{strconv.FormatFloat(float64(v), 'f', -1, 32)}, nil
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case json.Number:
		return []string{v.String()}, nil
	default:
		data, err := yaml.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("序列化配置值失败: %w", err)
		}
		text := strings.TrimRight(string(data), "\n")
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if text == "" {
			return []string{"null"}, nil
		}
		return strings.Split(text, "\n"), nil
	}
}

// indentYAMLLines 给块内每一行加缩进，空行保持空行。
func indentYAMLLines(lines []string, indent int, cr bool) []yamlPatchLine {
	pad := strings.Repeat(" ", indent)
	out := make([]yamlPatchLine, 0, len(lines))
	for _, line := range lines {
		body := line
		if body != "" {
			body = pad + body
		}
		out = append(out, yamlPatchLine{body: body, cr: cr})
	}
	return out
}

// buildYAMLNestedSnippet 生成「路径: 值」的嵌套片段。标量写成 key: value，
// map / 列表写成标准的块式 YAML。
func buildYAMLNestedSnippet(path []string, value interface{}, baseIndent int, cr bool) ([]yamlPatchLine, error) {
	valueLines, err := serializeYAMLPatchValue(value)
	if err != nil {
		return nil, err
	}
	scalar := isYAMLScalar(value) || (len(valueLines) == 1 && (valueLines[0] == "{}" || valueLines[0] == "[]"))

	cur := valueLines
	for i := len(path) - 1; i >= 0; i-- {
		key := path[i]
		next := make([]string, 0, len(cur)+1)
		if i == len(path)-1 && scalar {
			next = append(next, key+": "+cur[0])
			cur = next
			continue
		}
		next = append(next, key+":")
		for _, line := range cur {
			if line == "" {
				next = append(next, "")
			} else {
				next = append(next, "  "+line)
			}
		}
		cur = next
	}
	return indentYAMLLines(cur, baseIndent, cr), nil
}

// setYAMLInScope 在 lines 这个 YAML 映射块里按 path 改值。
// parentIndent 是上一级键的缩进（根为 -1）。
func setYAMLInScope(lines []yamlPatchLine, path []string, value interface{}, parentIndent int, cr bool) ([]yamlPatchLine, error) {
	childIndent := yamlPatchChildIndent(lines, parentIndent)

	keyIdx := -1
	var keyMatch []string
	for i, line := range lines {
		m, ok := parseYAMLKeyLine(line.body)
		if !ok {
			continue
		}
		indent := yamlPatchIndent(line.body)
		if childIndent >= 0 && indent != childIndent {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m[3]), path[0]) {
			keyIdx = i
			keyMatch = m
			break
		}
	}

	// 键不存在：在块尾（尾部空行之前）插入嵌套片段。
	if keyIdx < 0 {
		indent := childIndent
		if indent < 0 {
			indent = parentIndent + 2
			if indent < 0 {
				indent = 0
			}
		}
		snippet, err := buildYAMLNestedSnippet(path, value, indent, cr)
		if err != nil {
			return nil, err
		}
		insertAt := len(lines)
		for insertAt > 0 && yamlPatchBlank(lines[insertAt-1].body) {
			insertAt--
		}
		out := make([]yamlPatchLine, 0, len(lines)+len(snippet))
		out = append(out, lines[:insertAt]...)
		out = append(out, snippet...)
		out = append(out, lines[insertAt:]...)
		return out, nil
	}

	keyIndent := yamlPatchIndent(lines[keyIdx].body)
	blockEnd := yamlPatchBlockEnd(lines, keyIdx)
	sep := keyMatch[6]
	if sep == "" {
		// `key:` 后面必须留一个空格，否则 `key:"value"` 会被 YAML 当成
		// 一个普通标量（整行不是映射），配置项就静默失效了。
		sep = " "
	}
	prefix := keyMatch[1] + keyMatch[2] + keyMatch[3] + keyMatch[4] + keyMatch[5] + ":" + sep
	comment := keepInlineComment(keyMatch[7])

	// 目标就是这一层：替换值（标量直接换行，map/列表替换整块）。
	if len(path) == 1 {
		out := make([]yamlPatchLine, 0, len(lines))
		out = append(out, lines[:keyIdx]...)
		valueLines, err := serializeYAMLPatchValue(value)
		if err != nil {
			return nil, err
		}
		if isYAMLScalar(value) || (len(valueLines) == 1 && (valueLines[0] == "{}" || valueLines[0] == "[]")) {
			out = append(out, yamlPatchLine{body: prefix + valueLines[0] + comment, cr: lines[keyIdx].cr})
		} else {
			out = append(out, yamlPatchLine{body: prefix + comment, cr: lines[keyIdx].cr})
			out = append(out, indentYAMLLines(valueLines, keyIndent+2, cr)...)
		}
		out = append(out, lines[blockEnd:]...)
		return out, nil
	}

	// 还要往下走：已有的子块递归；没有子块（标量值）则整体换成嵌套片段。
	if blockEnd == keyIdx+1 {
		snippet, err := buildYAMLNestedSnippet(path[1:], value, keyIndent+2, cr)
		if err != nil {
			return nil, err
		}
		out := make([]yamlPatchLine, 0, len(lines)+len(snippet))
		out = append(out, lines[:keyIdx]...)
		out = append(out, yamlPatchLine{body: prefix + comment, cr: lines[keyIdx].cr})
		out = append(out, snippet...)
		out = append(out, lines[blockEnd:]...)
		return out, nil
	}

	block := lines[keyIdx+1 : blockEnd]
	newBlock, err := setYAMLInScope(block, path[1:], value, keyIndent, cr)
	if err != nil {
		return nil, err
	}
	out := make([]yamlPatchLine, 0, len(lines)-len(block)+len(newBlock))
	out = append(out, lines[:keyIdx+1]...)
	out = append(out, newBlock...)
	out = append(out, lines[blockEnd:]...)
	return out, nil
}

// lookupYAMLPath 判断路径在解析后的配置里是否存在（用于写入后的自检）。
func lookupYAMLPath(root map[string]interface{}, path []string) bool {
	var cur interface{} = root
	for _, key := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return false
		}
		cur, ok = m[key]
		if !ok {
			return false
		}
	}
	return true
}

// writeYAMLTextAtomic 以临时文件 + 原子替换写回配置。
func writeYAMLTextAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".patch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	const maxRenameRetries = 3
	var renameErr error
	for attempt := 0; attempt < maxRenameRetries; attempt++ {
		renameErr = os.Rename(tmpName, path)
		if renameErr == nil {
			tmpName = ""
			return nil
		}
		// Windows 上编辑器 / 杀毒软件可能瞬时占用目标文件，退避重试
		time.Sleep(time.Duration(150*(1<<attempt)) * time.Millisecond)
	}
	return fmt.Errorf("原子替换配置文件失败（已重试 %d 次）: %w", maxRenameRetries, renameErr)
}

// keepYAMLCommentsOnly 只保留注释与空行。
// 空文件 / 只有注释 / 字面 null / 空映射（{}）都没有可用的顶层键：
// 直接在这些文本后面追加映射会得到 `null\nbot:`、`{}\nbot:` 这种非法或
// 「解析不到新键」的 YAML，因此先把这些占位文档行去掉，只留注释。
func keepYAMLCommentsOnly(content string) string {
	lines := splitYAMLPatchLines(content)
	out := make([]yamlPatchLine, 0, len(lines))
	for _, line := range lines {
		if yamlPatchBlank(line.body) || yamlPatchComment(line.body) {
			out = append(out, line)
		}
	}
	return joinYAMLPatchLines(out)
}

// WriteConfigPatchesToPath 是显式路径版本：不持有全局写锁，
// 串行由调用方（admin 的配置写锁）负责。
//
// 写入前会先解析一遍新内容，确认仍是合法 YAML 且所有补丁键都存在；
// 任何一步失败都不会动原文件。
func WriteConfigPatchesToPath(patches []ConfigPatch, cfgPath string) error {
	if len(patches) == 0 {
		return nil
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var fileMode os.FileMode = 0o600 // 配置文件含凭据，新建时只给属主读写
	if info, err := os.Stat(cfgPath); err == nil {
		fileMode = info.Mode()
	}
	content := strings.TrimPrefix(string(data), "\ufeff")
	cr := strings.Contains(content, "\r\n")

	// 先确认现有内容是合法 YAML 映射；空文档 / null / {} 只保留注释后再写。
	var before map[string]interface{}
	if content != "" {
		if err := yaml.Unmarshal([]byte(content), &before); err != nil {
			return fmt.Errorf("现有配置解析失败，已拒绝写入: %w", err)
		}
	}
	if before == nil || len(before) == 0 {
		content = keepYAMLCommentsOnly(content)
	}

	lines := splitYAMLPatchLines(content)
	for _, patch := range patches {
		if len(patch.Path) == 0 {
			return fmt.Errorf("配置路径不能为空")
		}
		lines, err = setYAMLInScope(lines, patch.Path, patch.Value, -1, cr)
		if err != nil {
			return err
		}
	}
	out := joinYAMLPatchLines(lines)

	var after map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &after); err != nil {
		return fmt.Errorf("生成的新配置不是合法 YAML，已拒绝写入: %w", err)
	}
	// 整段丢失是编辑器最危险的 bug，宁可拒绝写入让用户保留旧配置。
	for key := range before {
		if _, ok := after[key]; !ok {
			return fmt.Errorf("内部错误：写入会丢失配置段 %q，已拒绝写入", key)
		}
	}
	for _, patch := range patches {
		if !lookupYAMLPath(after, patch.Path) {
			return fmt.Errorf("内部错误：配置键 %s 未写入，已拒绝写入", strings.Join(patch.Path, "."))
		}
	}

	return writeYAMLTextAtomic(cfgPath, []byte(out), fileMode)
}

// FlattenConfigPatch 把嵌套的配置补丁展开成叶子路径，供 WriteConfigPatchesToPath 使用。
// map 递归展开；空 map / 列表 / 标量作为叶子整体替换。
func FlattenConfigPatch(patch map[string]interface{}) []ConfigPatch {
	var out []ConfigPatch
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := patch[key]
		if sub, ok := value.(map[string]interface{}); ok && len(sub) > 0 {
			for _, child := range FlattenConfigPatch(sub) {
				path := make([]string, 0, len(child.Path)+1)
				path = append(path, key)
				path = append(path, child.Path...)
				out = append(out, ConfigPatch{Path: path, Value: child.Value})
			}
			continue
		}
		out = append(out, ConfigPatch{Path: []string{key}, Value: value})
	}
	return out
}
