package cfg

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
)

func validBoolean(value interface{}) bool {
	switch value := value.(type) {
	case bool:
		return true
	case string:
		_, err := strconv.ParseBool(value)
		return err == nil
	default:
		return false
	}
}

func integerValue(value interface{}) (int64, bool) {
	if value == nil {
		return 0, false
	}
	if str, ok := value.(string); ok {
		n, err := strconv.ParseInt(str, 10, 64)
		return n, err == nil
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := v.Uint()
		return int64(n), n <= math.MaxInt64
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		return int64(n), !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n && n >= math.MinInt64 && n < math.MaxInt64
	default:
		return 0, false
	}
}

func validInterval(value interface{}, seconds bool) bool {
	if value == nil {
		return true
	}
	text := ""
	if str, ok := value.(string); ok {
		text = str
		if seconds {
			text = strings.TrimSpace(str)
		}
		if text == "" {
			return true
		}
		if interval, err := time.ParseDuration(text); err == nil {
			return interval >= 0
		}
	} else {
		kind := reflect.TypeOf(value).Kind()
		switch kind {
		case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Float32, reflect.Float64:
			text = fmt.Sprint(value)
		default:
			return false
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return false
	}
	if seconds {
		return n >= 0 && n < float64(math.MaxInt64)/float64(time.Second)
	}
	// Other consumers historically interpret unqualified numbers as nanoseconds;
	// only zero is accepted without a duration unit, to select the default.
	return n == 0
}

func validateHotValues(s *config.Snapshot) error {
	for _, key := range []string{
		"logLevel", "ffmpeg.logLevel", "bot.commandPrefix", "bot.onJoinGroup.rename", "bot.onDisconnected",
		"twitter.translate.targetLang", "twitcasting.nameStrategy",
	} {
		if value := s.Get(key); value != nil {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s 必须是字符串", key)
			}
		}
	}
	for _, key := range []string{
		"bot.offlineQueue.enable", "bilibili.unsub", "bilibili.hiddenSub", "bilibili.disableSub",
		"bilibili.autoParsePosts", "bilibili.secAnalysis", "bilibili.onlyOnlineNotify", "acfun.unsub",
		"acfun.disableSub", "acfun.onlyOnlineNotify", "douyin.onlyOnlineNotify", "weibo.onlyOnlineNotify",
		"weibo.disableCookieAlert", "youtube.onlyOnlineNotify", "xhs.onlyOnlineNotify", "heybox.onlyOnlineNotify",
		"twitter.unsub", "twitter.retweetFullText", "twitter.translate.enabled", "twitcasting.broadcaster.title",
		"twitcasting.broadcaster.created", "twitcasting.broadcaster.image",
	} {
		if value := s.Get(key); value != nil && !validBoolean(value) {
			return fmt.Errorf("%s 必须是布尔值", key)
		}
	}
	if value := s.Get("platform"); value != nil {
		platforms, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("platform 必须是平台配置映射")
		}
		for site, value := range platforms {
			if value == nil {
				continue
			}
			options, ok := value.(map[string]interface{})
			if !ok {
				return fmt.Errorf("platform.%s 必须是配置映射", site)
			}
			if enabled := options["enable"]; enabled != nil && !validBoolean(enabled) {
				return fmt.Errorf("platform.%s.enable 必须是布尔值", site)
			}
		}
	}
	for _, key := range []string{"bilibili.minFollowerCap", "dispatch.largeNotifyLimit"} {
		if value := s.Get(key); value != nil {
			if _, ok := integerValue(value); !ok {
				return fmt.Errorf("%s 必须是整数", key)
			}
		}
	}
	for _, key := range []string{"autoreply.group.command", "autoreply.private.command", "debug.group", "debug.uin"} {
		value := s.Get(key)
		if value == nil {
			continue
		}
		if _, ok := value.(string); ok {
			continue
		}
		v := reflect.ValueOf(value)
		if v.Kind() != reflect.Slice {
			return fmt.Errorf("%s 必须是列表", key)
		}
		for i := 0; i < v.Len(); i++ {
			item := v.Index(i).Interface()
			if _, ok := item.(string); ok {
				continue
			}
			if strings.HasPrefix(key, "debug.") {
				if _, ok := integerValue(item); ok {
					continue
				}
			}
			return fmt.Errorf("%s[%d] 类型无效", key, i)
		}
	}
	for _, key := range []string{"concern.emitInterval", "bot.offlineQueue.expire", "twitter.translate.minInterval", "twitter.translate.batchWait"} {
		if !validInterval(s.Get(key), false) {
			return fmt.Errorf("%s 必须是非负时间间隔，例如 5s", key)
		}
	}
	return validateImageMerge(s)
}

func validImageMode(value interface{}) bool {
	if value == nil {
		return true
	}
	if _, ok := value.(bool); ok {
		return true
	}
	if str, ok := value.(string); ok {
		switch strings.ToLower(strings.TrimSpace(str)) {
		case "", "inherit", "auto", "only9", "always", "off", "true", "false":
			return true
		}
	}
	return false
}

func validateImageMerge(s *config.Snapshot) error {
	if !validImageMode(s.Get("bilibili.imageMergeMode")) {
		return fmt.Errorf("bilibili.imageMergeMode 的值或类型无效")
	}
	bases := map[string]bool{}
	for _, key := range s.AllKeys() {
		parts := strings.Split(key, ".")
		if parts[0] == "imagemerge" {
			bases["imageMerge"] = true
		}
		if len(parts) > 1 && parts[1] == "imagemerge" {
			bases[parts[0]+".imageMerge"] = true
		}
	}
	for base := range bases {
		if value := s.Get(base); value != nil {
			if _, ok := value.(map[string]interface{}); !ok {
				return fmt.Errorf("%s 必须是配置映射", base)
			}
		}
		for _, field := range []string{"mode", "layout", "maxWidth", "maxHeight"} {
			key := base + "." + field
			value := s.Get(key)
			if value == nil {
				continue
			}
			str, isString := value.(string)
			text := strings.ToLower(strings.TrimSpace(str))
			if isString && (text == "" || text == "inherit") {
				continue
			}
			switch field {
			case "mode":
				if validImageMode(value) {
					continue
				}
			case "layout":
				if isString && (text == "grid" || text == "long") {
					continue
				}
			default:
				if isString {
					value = text
				}
				if n, ok := integerValue(value); ok && n >= 0 {
					continue
				}
			}
			return fmt.Errorf("%s 的值或类型无效", key)
		}
	}
	return nil
}

func validateCronSchema(value interface{}) error {
	if value == nil {
		return nil
	}
	jobs := reflect.ValueOf(value)
	if jobs.Kind() != reflect.Slice {
		return fmt.Errorf("cronjob 必须是任务列表")
	}
	for i := 0; i < jobs.Len(); i++ {
		job, ok := jobs.Index(i).Interface().(map[string]interface{})
		if !ok {
			return fmt.Errorf("cronjob[%d] 必须是配置映射", i)
		}
		if _, ok := job["cron"].(string); !ok {
			return fmt.Errorf("cronjob[%d].cron 必须是字符串", i)
		}
		if name := job["templatename"]; name != nil {
			if _, ok := name.(string); !ok {
				return fmt.Errorf("cronjob[%d].templateName 必须是字符串", i)
			}
		}
		if job["target"] == nil {
			continue
		}
		target, ok := job["target"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("cronjob[%d].target 必须是配置映射", i)
		}
		for _, field := range []string{"group", "private"} {
			if target[field] == nil {
				continue
			}
			ids := reflect.ValueOf(target[field])
			if ids.Kind() != reflect.Slice {
				return fmt.Errorf("cronjob[%d].target.%s 必须是 ID 列表", i, field)
			}
			for j := 0; j < ids.Len(); j++ {
				if _, ok := integerValue(ids.Index(j).Interface()); !ok {
					return fmt.Errorf("cronjob[%d].target.%s[%d] 必须是整数 ID", i, field, j)
				}
			}
		}
	}
	return nil
}
