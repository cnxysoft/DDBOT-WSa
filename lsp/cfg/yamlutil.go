package cfg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// configWriteMu 全局配置文件写锁，所有配置写操作共用
// 防止并发写入导致文件损坏
var configWriteMu sync.Mutex

// GetConfigWriteMutex serializes read/modify/write operations across all writers.
func GetConfigWriteMutex() *sync.Mutex { return &configWriteMu }

// WriteConfigKeyValue safely updates a single key in application.yaml.
// The key uses dot notation (e.g. "weibo.alertGroupId").
// It locates YAML nodes and edits source ranges to preserve other content.
// Uses atomic write (temp file + rename) to prevent corruption.
func WriteConfigKeyValue(key, value string) error {
	return WriteConfigKeyValues(map[string]string{key: value})
}

// WriteConfigKeyValues updates related credentials in one validated transaction.
// No intermediate partial account or mode is written or published.
func WriteConfigKeyValues(values map[string]string) error {
	return WriteConfigKeyValuesContext(context.Background(), values)
}

// WriteConfigKeyValuesContext prevents a canceled login session from publishing
// credentials after waiting for another configuration transaction.
func WriteConfigKeyValuesContext(ctx context.Context, values map[string]string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return updateConfigKeyValues(values, ConfigFilePath(), func(data []byte, persist func() error) error {
		return config.GlobalConfig.SaveContext(ctx, data, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return persist()
		})
	})
}

// quoteYAMLValue 把值写成 YAML 双引号标量。
// 裸值遇到 ": "、" #"、首尾空格，或以 * & ! % @ 这类指示符开头时会解析错；
// cookie / token 又长又杂，统一加引号最省心。
func quoteYAMLValue(v string) string {
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf("\\x%02x", r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeConfigKeyValueToPath writes a config key-value pair to the specified file path.
// Must be called with configWriteMu held.
func writeConfigKeyValueToPath(key, value, cfgPath string) error {
	return updateConfigKeyValue(key, value, cfgPath, nil)
}

func validateYAML(data []byte) error {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("配置不是合法 YAML，已拒绝写入")
	}
	return nil
}

func updateConfigKeyValue(key, value, cfgPath string, save func([]byte, func() error) error) error {
	return updateConfigKeyValues(map[string]string{key: value}, cfgPath, save)
}

func updateConfigKeyValues(values map[string]string, cfgPath string, save func([]byte, func() error) error) error {
	version, err := ReadConfigFileVersion(cfgPath)
	if err != nil {
		return err
	}
	if !version.Exists() {
		return os.ErrNotExist
	}
	data := version.Bytes()
	if err := validateYAML(data); err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	newData := data
	for _, key := range keys {
		newData, err = rewriteConfigKeyValue(newData, key, values[key])
		if err != nil {
			return err
		}
	}
	// Parsing alone is insufficient: flat dotted keys and case-equivalent keys
	// can shadow an otherwise valid edit. Verify the complete credential pair
	// before either prepare hooks or the persistence callback can observe it.
	if err := validateUpdatedConfigValues(newData, values); err != nil {
		return err
	}
	persist := func() error {
		return version.Persist(newData, version.Mode(), false)
	}
	if save != nil {
		return save(newData, persist)
	}
	return persist()
}

// validateUpdatedConfigValues uses the same lookup rules as runtime snapshots.
// Never include credential values or parser diagnostics in a returned error.
func validateUpdatedConfigValues(data []byte, values map[string]string) error {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("配置不是合法 YAML，已拒绝写入")
	}
	for key, value := range values {
		if v.GetString(key) != value {
			return fmt.Errorf("配置键 %s 未能有效更新，已拒绝写入", key)
		}
	}
	return nil
}

// rewriteConfigKeyValue uses parsed node positions rather than matching text
// inside nested mappings or scalar bodies. Unrelated bytes remain untouched.
func rewriteConfigKeyValue(data []byte, key, value string) ([]byte, error) {
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid config key format: %s (expected section.name)", key)
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid config key format: %s", key)
		}
	}
	content := strings.TrimPrefix(string(data), "\ufeff")
	updated, err := rewriteYAMLPath(content, parts, quoteYAMLValue(value))
	if err != nil {
		return nil, fmt.Errorf("配置键 %s 无法安全更新，已拒绝写入: %w", key, err)
	}
	if bytes.HasPrefix(data, []byte("\ufeff")) {
		updated = "\ufeff" + updated
	}
	newData := []byte(updated)
	if err := validateUpdatedConfigValues(newData, map[string]string{key: value}); err != nil {
		return nil, err
	}
	return newData, nil
}
