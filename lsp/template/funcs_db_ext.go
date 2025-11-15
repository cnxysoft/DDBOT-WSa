package template

import (
	"encoding/json"
	"strings"
	"time"

	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
)

func decKeys(key string) (K []interface{}) {
	k := strings.Split(key, ":")
	for _, v := range k {
		K = append(K, v)
	}
	return
}

func decOptionsSlice(opts [][]localdb.OptionFunc) []localdb.OptionFunc {
	if len(opts) > 0 {
		return opts[0]
	}
	return nil
}

func Set(key string, value string, opts ...[]localdb.OptionFunc) string {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	err := GetTemplateSC().Set(localdb.ExtDbCustomKey(Keys...), value, opt...)
	if localdb.IsRollback(err) {
		return localdb.ErrKeyExist.Error()
	}
	if err != nil {
		logger.Errorf("ExtDB: set error: %v", err)
		return err.Error()
	}
	return ""
}

func Get(key string, opts ...[]localdb.OptionFunc) string {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	value, err := GetTemplateSC().Get(localdb.ExtDbCustomKey(Keys...), opt...)
	if err != nil {
		logger.Errorf("ExtDB: get error: %v", err)
		return err.Error()
	}
	return value
}

func setJson(key string, data interface{}, opts ...[]localdb.OptionFunc) string {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	err := GetTemplateSC().SetJson(localdb.ExtDbCustomKey(Keys...), data, opt...)
	if localdb.IsRollback(err) {
		return localdb.ErrKeyExist.Error()
	}
	if err != nil {
		logger.Errorf("ExtDB: set json error: %v", err)
		return err.Error()
	}
	return ""
}

func getJson(key string, opts ...[]localdb.OptionFunc) interface{} {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)

	var err error
	var raw json.RawMessage
	if err = GetTemplateSC().GetJson(localdb.ExtDbCustomKey(Keys...), &raw, opt...); err != nil {
		logger.Errorf("ExtDB: get json error: %v", err)
		return nil
	}
	if len(raw) == 0 {
		return nil
	}

	switch raw[0] {
	case '{': // 对象
		var obj map[string]interface{}
		if err = json.Unmarshal(raw, &obj); err == nil {
			return obj
		}
		logger.Errorf("ExtDB: parse object error: %v", err)
	case '[': // 数组
		var arr []interface{}
		if err = json.Unmarshal(raw, &arr); err == nil {
			return arr
		}
		logger.Errorf("ExtDB: parse array error: %v", err)
	default: // 原始值：string/number/bool/null
		var v interface{}
		if err = json.Unmarshal(raw, &v); err == nil {
			return v
		}
		logger.Errorf("ExtDB: parse primitive error: %v", err)
	}

	return nil
}

func setInt64(key string, value int64, opts ...[]localdb.OptionFunc) string {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	err := GetTemplateSC().SetInt64(localdb.ExtDbCustomKey(Keys...), value, opt...)
	if localdb.IsRollback(err) {
		return localdb.ErrKeyExist.Error()
	}
	if err != nil {
		logger.Errorf("ExtDB: set int64 error: %v", err)
		return err.Error()
	}
	return ""
}

func seqInt64(key string) int64 {
	Keys := decKeys(key)
	value, err := GetTemplateSC().SeqNext(localdb.ExtDbCustomKey(Keys...))
	if localdb.IsRollback(err) {
		return -1
	}
	if err != nil {
		logger.Errorf("ExtDB: set int64 error: %v", err)
		return -1
	}
	return value
}

func incInt64(key string, value int64) int64 {
	Keys := decKeys(key)
	Value, err := GetTemplateSC().IncInt64(localdb.ExtDbCustomKey(Keys...), value)
	if localdb.IsRollback(err) {
		return -1
	}
	if err != nil {
		logger.Errorf("ExtDB: set int64 error: %v", err)
		return -1
	}
	return Value
}

func getInt64(key string, opts ...[]localdb.OptionFunc) int64 {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	value, err := GetTemplateSC().GetInt64(localdb.ExtDbCustomKey(Keys...), opt...)
	if err != nil {
		logger.Errorf("ExtDB: get int64 error: %v", err)
		return 0
	}
	return value
}

func delData(key string, opts ...[]localdb.OptionFunc) string {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	_, err := GetTemplateSC().Delete(localdb.ExtDbCustomKey(Keys...), opt...)
	if err != nil {
		logger.Errorf("ExtDB: del error: %v", err)
		return err.Error()
	}
	return ""
}

func existData(key string, opts ...[]localdb.OptionFunc) bool {
	Keys := decKeys(key)
	opt := decOptionsSlice(opts)
	return GetTemplateSC().Exist(localdb.ExtDbCustomKey(Keys...), opt...)
}

func getOptions(opt []interface{}, param ...interface{}) []localdb.OptionFunc {
	var opts []localdb.OptionFunc
	for _, iopt := range opt {
		switch iopt {
		case "GetIgnoreExpire":
			opts = append(opts, localdb.GetIgnoreExpireOpt())
		case "IgnoreNotFound":
			opts = append(opts, localdb.IgnoreNotFoundOpt())
		case "GetTTL":
			if len(param) > 0 {
				if t, ok := param[0].(time.Duration); ok {
					opts = append(opts, localdb.GetTTLOpt(&t))
				}
			}
		case "SetExpire":
			if len(param) > 0 {
				t, err := time.ParseDuration(param[0].(string))
				if err != nil {
					logger.Errorf("ExtDB: get ttl error: %v", err)
					continue
				}
				opts = append(opts, localdb.SetExpireOpt(t))
			}
		case "SetKeepLastExpire":
			opts = append(opts, localdb.SetKeepLastExpireOpt())
		case "SetNoOverWrite":
			opts = append(opts, localdb.SetNoOverWriteOpt())
		case "SetGetPreviousValueInt64":
			if len(param) > 0 {
				if i, ok := param[0].(int64); ok {
					opts = append(opts, localdb.SetGetPreviousValueInt64Opt(&i))
				}
			}
		case "SetGetPreviousValueString":
			if len(param) > 0 {
				if s, ok := param[0].(string); ok {
					opts = append(opts, localdb.SetGetPreviousValueStringOpt(&s))
				}
			}
		case "SetGetPreviousValueJsonObject":
			if len(param) > 0 {
				if m, ok := param[0].(*map[string]interface{}); ok {
					opts = append(opts, localdb.SetGetPreviousValueJsonObjectOpt(m))
				}
			}
		case "SetGetIsOverwrite":
			if len(param) > 0 {
				if b, ok := param[0].(bool); ok {
					opts = append(opts, localdb.SetGetIsOverwriteOpt(&b))
				}
			}
		}
	}
	return opts
}

func newDuration() *time.Duration {
	var t time.Duration
	return &t
}
