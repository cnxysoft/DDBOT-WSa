package admin

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decoding a node also detects exact duplicate keys before the JSON-compatible
// YAML decoder can silently collapse them. Never return parser text to the API:
// it may include credentials from the source document.
func validateConfigDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil && err != io.EOF {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("配置只支持一个 YAML 文档")
	}
	var decoded interface{}
	return document.Decode(&decoded)
}

// These maps contain user-defined names, not configuration field names.
func literalConfigMap(path string) bool {
	switch strings.ToLower(path) {
	case "customcommandprefix", "telegram.commands", "xhs.cookies":
		return true
	}
	return false
}

func configMapKey(m map[string]interface{}, key string) (string, bool) {
	for existing := range m {
		if strings.ToLower(existing) == strings.ToLower(key) {
			return existing, true
		}
	}
	return key, false
}

// normalizeConfigPaths gives policy checks and persistence the same field
// identity as Viper. Preserve spelling and literal data keys, expand dotted
// setting paths, and reject aliases that would make runtime selection ambiguous.
func normalizeConfigPaths(input map[string]interface{}) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	declared := make(map[string]bool)
	var insert func(map[string]interface{}, string, interface{}, string) error
	insert = func(target map[string]interface{}, key string, value interface{}, prefix string) error {
		parts := strings.Split(key, ".")
		for i, part := range parts {
			if part == "" {
				return fmt.Errorf("配置字段路径不能为空")
			}
			actual, present := configMapKey(target, part)
			path := joinConfigPath(prefix, actual)
			identity := strings.ToLower(path)
			last := i == len(parts)-1
			if literalConfigMap(path) && !last {
				return fmt.Errorf("%s 请使用嵌套对象，映射内的名称按原样保存", path)
			}
			if last {
				if declared[identity] {
					return fmt.Errorf("配置字段 %s 重复，请合并大小写或点号等价的配置", path)
				}
				declared[identity] = true
				if child, ok := value.(map[string]interface{}); ok && !literalConfigMap(path) {
					var nested map[string]interface{}
					if present {
						var valid bool
						nested, valid = target[actual].(map[string]interface{})
						if !valid {
							return fmt.Errorf("配置字段 %s 同时作为值和父路径，请使用唯一结构", path)
						}
					} else {
						nested = make(map[string]interface{})
						target[actual] = nested
					}
					for _, name := range sortedConfigKeys(child) {
						if err := insert(nested, name, child[name], path); err != nil {
							return err
						}
					}
				} else {
					if present {
						return fmt.Errorf("配置字段 %s 同时作为值和父路径，请使用唯一结构", path)
					}
					target[actual] = value
				}
				return nil
			}
			if present {
				child, ok := target[actual].(map[string]interface{})
				if !ok {
					return fmt.Errorf("配置字段 %s 同时作为值和父路径，请使用唯一结构", path)
				}
				target = child
			} else {
				child := make(map[string]interface{})
				target[actual] = child
				target = child
			}
			prefix = path
		}
		return nil
	}
	for _, key := range sortedConfigKeys(input) {
		if err := insert(out, key, input[key], ""); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func sortedConfigKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
