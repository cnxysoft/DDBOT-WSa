package weibo

import (
	"bytes"
	stdjson "encoding/json"
	"sort"
	"strconv"
	"strings"
)

func (c *Card) UnmarshalJSON(data []byte) error {
	type cardAlias Card

	raw := map[string]stdjson.RawMessage{}
	if err := stdjson.Unmarshal(data, &raw); err != nil {
		return err
	}

	coerceInt64JSONField(raw, "id")
	normalizePageInfoObjectType(raw)
	normalizePicsToPicInfos(raw)

	normalized, err := stdjson.Marshal(raw)
	if err != nil {
		return err
	}

	var alias cardAlias
	if err := stdjson.Unmarshal(normalized, &alias); err != nil {
		return err
	}

	*c = Card(alias)
	return nil
}

func (u *ApiContainerGetIndexProfileResponse_Data_UserInfo) UnmarshalJSON(data []byte) error {
	type userAlias ApiContainerGetIndexProfileResponse_Data_UserInfo

	raw := map[string]stdjson.RawMessage{}
	if err := stdjson.Unmarshal(data, &raw); err != nil {
		return err
	}

	coerceInt64JSONField(raw, "id")

	normalized, err := stdjson.Marshal(raw)
	if err != nil {
		return err
	}

	var alias userAlias
	if err := stdjson.Unmarshal(normalized, &alias); err != nil {
		return err
	}

	*u = ApiContainerGetIndexProfileResponse_Data_UserInfo(alias)
	return nil
}

func coerceInt64JSONField(raw map[string]stdjson.RawMessage, key string) {
	value, exists := raw[key]
	if !exists {
		return
	}

	parsed, ok := parseInt64JSONValue(value)
	if !ok {
		raw[key] = []byte("0")
		return
	}

	raw[key] = []byte(strconv.FormatInt(parsed, 10))
}

func normalizePageInfoObjectType(raw map[string]stdjson.RawMessage) {
	pageInfoRaw, exists := raw["page_info"]
	if !exists {
		return
	}

	pageInfo := map[string]stdjson.RawMessage{}
	if err := stdjson.Unmarshal(pageInfoRaw, &pageInfo); err != nil {
		return
	}

	if parsed, ok := parseStringJSONField(pageInfo, "type"); ok && strings.EqualFold(parsed, "topic") {
		pageInfo["page_pic"] = []byte(`""`)
	}

	coerceStringJSONField(pageInfo, "object_type")
	normalizePagePicField(pageInfo, "page_pic")

	normalizedPageInfo, err := stdjson.Marshal(pageInfo)
	if err != nil {
		return
	}

	raw["page_info"] = normalizedPageInfo
}

func normalizePagePicField(raw map[string]stdjson.RawMessage, key string) {
	value, exists := raw[key]
	if !exists {
		return
	}

	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		raw[key] = []byte(`""`)
		return
	}

	if trimmed[0] == '{' {
		pagePic := map[string]stdjson.RawMessage{}
		if err := stdjson.Unmarshal(trimmed, &pagePic); err != nil {
			raw[key] = []byte(`""`)
			return
		}

		for _, candidate := range []string{"url", "source", "pic", "large", "largest", "bmiddle"} {
			if parsed, ok := parsePagePicURLField(pagePic, candidate); ok {
				raw[key] = encodeStringJSONValue(parsed)
				return
			}
		}

		if parsed, ok := findFirstHTTPStringField(pagePic); ok {
			raw[key] = encodeStringJSONValue(parsed)
			return
		}

		raw[key] = []byte(`""`)
		return
	}

	parsed, ok := parseStringJSONValue(trimmed)
	if !ok || !looksLikeHTTPURL(parsed) {
		raw[key] = []byte(`""`)
		return
	}

	raw[key] = encodeStringJSONValue(parsed)
}

func parsePagePicURLField(raw map[string]stdjson.RawMessage, key string) (string, bool) {
	value, exists := raw[key]
	if !exists {
		return "", false
	}

	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}

	if parsed, ok := parseStringJSONValue(trimmed); ok && looksLikeHTTPURL(parsed) {
		return parsed, true
	}

	if trimmed[0] != '{' {
		return "", false
	}

	nested := map[string]stdjson.RawMessage{}
	if err := stdjson.Unmarshal(trimmed, &nested); err != nil {
		return "", false
	}

	parsed, ok := parseStringJSONField(nested, "url")
	if !ok || !looksLikeHTTPURL(parsed) {
		return "", false
	}

	return parsed, true
}

func findFirstHTTPStringField(raw map[string]stdjson.RawMessage) (string, bool) {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if parsed, ok := parseStringJSONField(raw, key); ok && looksLikeHTTPURL(parsed) {
			return parsed, true
		}
	}

	return "", false
}

func looksLikeHTTPURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func normalizePicsToPicInfos(raw map[string]stdjson.RawMessage) {
	if _, exists := raw["pic_infos"]; exists {
		return
	}

	picsRaw, exists := raw["pics"]
	if !exists {
		return
	}

	var pics []map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(picsRaw, &pics); err != nil {
		return
	}

	picInfos := map[string]map[string]stdjson.RawMessage{}
	for index, pic := range pics {
		key := strconv.Itoa(index)
		if parsed, ok := parseStringJSONField(pic, "pic_id"); ok {
			key = parsed
		} else if parsed, ok := parseStringJSONField(pic, "pid"); ok {
			key = parsed
		}

		picInfo := map[string]stdjson.RawMessage{}
		if parsed, ok := parseStringJSONField(pic, "type"); ok {
			picInfo["type"] = encodeStringJSONValue(parsed)
		}

		largeURL := ""
		if largeRaw, exists := pic["large"]; exists {
			large := map[string]stdjson.RawMessage{}
			if err := stdjson.Unmarshal(largeRaw, &large); err == nil {
				if parsed, ok := parseStringJSONField(large, "url"); ok {
					largeURL = parsed
				}
			}
		}

		if largeURL == "" {
			if parsed, ok := parseStringJSONField(pic, "url"); ok {
				largeURL = parsed
			}
		}

		if largeURL != "" {
			picInfo["large"] = encodeRawJSONObject(map[string]stdjson.RawMessage{
				"url": encodeStringJSONValue(largeURL),
			})
		}

		picInfos[key] = picInfo
	}

	if len(picInfos) == 0 {
		return
	}

	raw["pic_infos"] = encodeRawJSONObject(picInfos)
}

func coerceStringJSONField(raw map[string]stdjson.RawMessage, key string) {
	value, exists := raw[key]
	if !exists {
		return
	}

	parsed, ok := parseStringJSONValue(value)
	if !ok {
		raw[key] = []byte(`""`)
		return
	}

	encoded, err := stdjson.Marshal(parsed)
	if err != nil {
		raw[key] = []byte(`""`)
		return
	}

	raw[key] = encoded
}

func parseStringJSONValue(raw stdjson.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}

	if trimmed[0] == '"' {
		var value string
		if err := stdjson.Unmarshal(trimmed, &value); err != nil {
			return "", false
		}
		return value, true
	}

	decoder := stdjson.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}

	number, ok := value.(stdjson.Number)
	if !ok {
		return "", false
	}

	return number.String(), true
}

func parseStringJSONField(raw map[string]stdjson.RawMessage, key string) (string, bool) {
	value, exists := raw[key]
	if !exists {
		return "", false
	}

	return parseStringJSONValue(value)
}

func encodeStringJSONValue(value string) stdjson.RawMessage {
	encoded, err := stdjson.Marshal(value)
	if err != nil {
		return []byte(`""`)
	}

	return encoded
}

func encodeRawJSONObject(value any) stdjson.RawMessage {
	encoded, err := stdjson.Marshal(value)
	if err != nil {
		return []byte("{}")
	}

	return encoded
}

func parseInt64JSONValue(raw stdjson.RawMessage) (int64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}

	if trimmed[0] == '"' {
		var value string
		if err := stdjson.Unmarshal(trimmed, &value); err != nil {
			return 0, false
		}
		if value == "" {
			return 0, false
		}

		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}

	decoder := stdjson.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, false
	}

	number, ok := value.(stdjson.Number)
	if !ok {
		return 0, false
	}

	parsed, err := number.Int64()
	if err != nil {
		return 0, false
	}

	return parsed, true
}
