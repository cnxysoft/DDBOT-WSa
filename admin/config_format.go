package admin

import (
	"reflect"
	"sort"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/ghodss/yaml"
)

// Keep the user's comments and whitespace when editing them in place gives
// exactly the same document as our normalized, validated configuration merge.
// Complex YAML (e.g. dotted keys or aliases) can require a full rewrite.
func renderConfigCandidate(original []byte, candidate map[string]interface{}) ([]byte, error) {
	canonical, err := yaml.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	var before, want map[string]interface{}
	if yaml.Unmarshal(original, &before) != nil || yaml.Unmarshal(canonical, &want) != nil {
		return canonical, nil
	}
	patched, err := cfg.RenderConfigPatches(original, configFormatDiff(before, want, nil))
	if err == nil && validateConfigDocument(patched) == nil {
		var after map[string]interface{}
		if yaml.Unmarshal(patched, &after) == nil && reflect.DeepEqual(after, want) {
			return patched, nil
		}
	}
	return canonical, nil
}

func configFormatDiff(before, after map[string]interface{}, prefix []string) []cfg.ConfigPatch {
	var patches []cfg.ConfigPatch
	keys := make([]string, 0, len(after))
	for key := range after {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := after[key]
		if old, exists := before[key]; exists && reflect.DeepEqual(old, value) {
			continue
		}
		path := append(append([]string(nil), prefix...), key)
		old, oldMap := before[key].(map[string]interface{})
		next, nextMap := value.(map[string]interface{})
		if oldMap && nextMap && !literalConfigMap(strings.Join(path, ".")) {
			onlyAddOrReplace := true
			for child := range old {
				if _, exists := next[child]; !exists {
					onlyAddOrReplace = false
					break
				}
			}
			if onlyAddOrReplace {
				patches = append(patches, configFormatDiff(old, next, path)...)
				continue
			}
		}
		patches = append(patches, cfg.ConfigPatch{Path: path, Value: value})
	}
	return patches
}
