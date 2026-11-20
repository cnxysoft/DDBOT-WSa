package cfg

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// YAML nodes identify the actual configuration path; edits use source offsets
// so comments, key spelling and each existing line ending remain unchanged.
func rewriteYAMLPath(content string, parts []string, quoted string) (string, error) {
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil && err != io.EOF {
		return "", errors.New("YAML 解析失败")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("不支持多个 YAML 文档")
	}
	if len(doc.Content) == 0 {
		return insertYAMLBlock(content, len(content), 0, parts, quoted), nil
	}
	return rewriteYAMLMapping(content, doc.Content[0], len(content), parts, quoted)
}

func rewriteYAMLMapping(content string, mapping *yaml.Node, boundary int, parts []string, quoted string) (string, error) {
	if mapping.Kind != yaml.MappingNode || mapping.Anchor != "" || mapping.Style&yaml.TaggedStyle != 0 {
		return "", errors.New("配置路径必须是无锚点的映射")
	}
	match, consumed := -1, 0
	for i := 0; i < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode {
			return "", errors.New("不支持复合 YAML 键")
		}
		for count := 1; count <= len(parts); count++ {
			if strings.EqualFold(key.Value, strings.Join(parts[:count], ".")) {
				if match >= 0 {
					return "", errors.New("存在大小写等价或平铺与嵌套冲突的配置路径")
				}
				match, consumed = i, count
			}
		}
	}
	if match < 0 {
		// An inherited target cannot be located in this source mapping safely.
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == "<<" {
				return "", errors.New("不支持通过 YAML 合并键继承的配置路径")
			}
		}
		if mapping.Style&yaml.FlowStyle != 0 {
			return insertYAMLFlow(content, mapping, parts, quoted)
		}
		indent := mapping.Column - 1
		if len(mapping.Content) > 0 {
			indent = mapping.Content[0].Column - 1
		}
		return insertYAMLBlock(content, boundary, indent, parts, quoted), nil
	}
	key, value := mapping.Content[match], mapping.Content[match+1]
	if consumed == len(parts) {
		return replaceYAMLScalar(content, key, value, mapping.Style&yaml.FlowStyle != 0, quoted)
	}
	if mapping.Style&yaml.FlowStyle == 0 && match+2 < len(mapping.Content) {
		boundary = yamlLineOffset(content, mapping.Content[match+2].Line)
	}
	if value.Kind == yaml.ScalarNode && value.Tag == "!!null" && value.Value == "" && value.Anchor == "" {
		// A block `section:` with an empty value is an empty section. Flow nulls
		// need a different edit, so refuse them rather than changing the parent.
		if mapping.Style&yaml.FlowStyle != 0 {
			return "", errors.New("不支持流式映射中的空父路径")
		}
		return insertYAMLBlock(content, boundary, key.Column+1, parts[consumed:], quoted), nil
	}
	return rewriteYAMLMapping(content, value, boundary, parts[consumed:], quoted)
}

func replaceYAMLScalar(content string, key, value *yaml.Node, flow bool, quoted string) (string, error) {
	if value.Kind != yaml.ScalarNode || value.Anchor != "" ||
		value.Style&(yaml.TaggedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 || value.Line != key.Line {
		return "", errors.New("不支持锚点、别名、复合值或多行凭据值")
	}
	start, err := yamlNodeOffset(content, key)
	if err != nil {
		return "", err
	}
	colon := start
	if content[start] == '\'' || content[start] == '"' {
		colon, err = yamlQuotedEnd(content, start)
		if err != nil {
			return "", err
		}
	} else {
		for colon < len(content) && content[colon] != ':' && content[colon] != '\n' {
			colon++
		}
	}
	for colon < len(content) && (content[colon] == ' ' || content[colon] == '\t') {
		colon++
	}
	if colon >= len(content) || content[colon] != ':' {
		return "", errors.New("无法定位配置键的冒号")
	}
	start = colon + 1
	for start < len(content) && (content[start] == ' ' || content[start] == '\t') {
		start++
	}
	end := start
	if start < len(content) && (content[start] == '\'' || content[start] == '"') {
		end, err = yamlQuotedEnd(content, start)
		if err != nil {
			return "", err
		}
	} else {
		for end < len(content) && content[end] != '\n' && content[end] != '\r' {
			if flow && strings.ContainsRune(",}]", rune(content[end])) {
				break
			}
			if content[end] == '#' && (end == start || content[end-1] == ' ' || content[end-1] == '\t') {
				break
			}
			end++
		}
		for end > start && (content[end-1] == ' ' || content[end-1] == '\t') {
			end--
		}
	}
	if start == colon+1 {
		quoted = " " + quoted
	}
	if start == end && start < len(content) && content[start] == '#' {
		quoted += " "
	}
	return content[:start] + quoted + content[end:], nil
}

// yamlQuotedEnd only accepts a token on one physical line. Multiline quoted
// credentials are refused; replacing only their first line would corrupt text.
func yamlQuotedEnd(content string, start int) (int, error) {
	quote := content[start]
	for i := start + 1; i < len(content); i++ {
		if content[i] == '\n' || content[i] == '\r' {
			break
		}
		if quote == '"' && content[i] == '\\' {
			i++
			continue
		}
		if content[i] == quote {
			if quote == '\'' && i+1 < len(content) && content[i+1] == '\'' {
				i++
				continue
			}
			return i + 1, nil
		}
	}
	return 0, errors.New("不支持多行引号键或值")
}

func insertYAMLBlock(content string, boundary, indent int, parts []string, quoted string) string {
	// Blank lines may belong to a preceding |+ scalar. Insert after them rather
	// than moving them across the new key and changing unrelated scalar text.
	eol := "\n"
	if strings.Contains(content, "\r\n") {
		eol = "\r\n"
	}
	var inserted strings.Builder
	if boundary > 0 && content[boundary-1] != '\n' {
		inserted.WriteString(eol)
	}
	for i, part := range parts {
		inserted.WriteString(strings.Repeat(" ", indent+2*i))
		inserted.WriteString(yamlPathKey(part))
		inserted.WriteByte(':')
		if i == len(parts)-1 {
			inserted.WriteByte(' ')
			inserted.WriteString(quoted)
		}
		inserted.WriteString(eol)
	}
	return content[:boundary] + inserted.String() + content[boundary:]
}

func insertYAMLFlow(content string, mapping *yaml.Node, parts []string, quoted string) (string, error) {
	start, err := yamlNodeOffset(content, mapping)
	if err != nil || content[start] != '{' {
		return "", errors.New("无法定位流式映射")
	}
	depth, lastToken := 0, byte(0)
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '\'', '"':
			i, err = yamlQuotedEnd(content, i)
			if err != nil {
				return "", err
			}
			i--
			lastToken = 'v'
			continue
		case '#':
			if i > 0 && (content[i-1] == ' ' || content[i-1] == '\t' || content[i-1] == '\n') {
				for i < len(content) && content[i] != '\n' {
					i++
				}
				continue
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				entry := quoted
				for j := len(parts) - 1; j > 0; j-- {
					entry = "{" + yamlPathKey(parts[j]) + ": " + entry + "}"
				}
				entry = yamlPathKey(parts[0]) + ": " + entry
				if len(mapping.Content) > 0 && lastToken != ',' {
					entry = ", " + entry
				}
				return content[:i] + entry + content[i:], nil
			}
		}
		if !strings.ContainsRune(" \t\r\n", rune(content[i])) {
			lastToken = content[i]
		}
	}
	return "", errors.New("无法定位流式映射的结束位置")
}

func yamlPathKey(key string) string {
	for _, ch := range key {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') &&
			!(ch >= '0' && ch <= '9') && ch != '_' && ch != '-' {
			return quoteYAMLValue(key)
		}
	}
	return key
}

func yamlLineOffset(content string, line int) int {
	position := 0
	for n := 1; n < line; n++ {
		end := strings.IndexByte(content[position:], '\n')
		if end < 0 {
			return len(content)
		}
		position += end + 1
	}
	return position
}

func yamlNodeOffset(content string, node *yaml.Node) (int, error) {
	position := yamlLineOffset(content, node.Line)
	for column := 1; column < node.Column && position < len(content); column++ {
		_, size := utf8.DecodeRuneInString(content[position:])
		position += size
	}
	if position >= len(content) {
		return 0, errors.New("无法定位 YAML 节点")
	}
	return position, nil
}
