package xhs

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"gopkg.in/yaml.v3"
)

func cookiesFromSnapshot(s *config.Snapshot) map[string]string {
	cookies := make(map[string]string)
	names := make(map[string]string)
	var document yaml.Node
	if yaml.Unmarshal(s.RawYAML(), &document) == nil && len(document.Content) > 0 {
		root := document.Content[0]
		if root.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(root.Content); i += 2 {
				if !strings.EqualFold(root.Content[i].Value, "xhs") {
					continue
				}
				xhs := root.Content[i+1]
				for j := 0; xhs.Kind == yaml.MappingNode && j+1 < len(xhs.Content); j += 2 {
					if !strings.EqualFold(xhs.Content[j].Value, "cookies") {
						continue
					}
					mapping := xhs.Content[j+1]
					for k := 0; mapping.Kind == yaml.MappingNode && k+1 < len(mapping.Content); k += 2 {
						name := mapping.Content[k].Value
						names[strings.ToLower(name)] = name
					}
				}
			}
		}
	}
	if raw, ok := s.Get("xhs.cookies").(map[string]interface{}); ok {
		for key, value := range raw {
			if text, ok := value.(string); ok && text != "" {
				name := names[strings.ToLower(key)]
				if name == "" {
					name = key
				}
				if strings.EqualFold(name, "webid") {
					name = "webId"
				}
				cookies[name] = text
			}
		}
	}
	return cookies
}

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	if raw := next.Get("xhs.cookies"); raw != nil {
		mapping, ok := raw.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("xhs.cookies 必须为字符串键值表")
		}
		for _, value := range mapping {
			if _, ok := value.(string); !ok {
				return nil, nil, fmt.Errorf("xhs.cookies 值必须为字符串")
			}
		}
	}
	oldCookies, newCookies := cookiesFromSnapshot(previous), cookiesFromSnapshot(next)
	if reflect.DeepEqual(oldCookies, newCookies) {
		return nil, nil, nil
	}
	for key, value := range newCookies {
		if key == "" || strings.ContainsAny(key, " ()<>@,;:\"/[]?={}\t\r\n") {
			return nil, nil, fmt.Errorf("小红书 Cookie 名称无效")
		}
		if strings.ContainsAny(value, "\r\n;") {
			return nil, nil, fmt.Errorf("小红书 Cookie 值不得包含换行或分号")
		}
	}
	return func() { registeredConcern.client.SetCookies(newCookies) }, nil, nil
}
