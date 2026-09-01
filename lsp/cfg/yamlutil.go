package cfg

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
)

// configWriteMu 全局配置文件写锁，所有配置写操作共用
// 防止并发写入导致文件损坏
var configWriteMu sync.Mutex

// writeIgnoreWindow 写入完成后的热重载忽略窗口。
// fsnotify 事件是异步送达的，写入结束后标志可能已经复位，
// 导致自己触发的变更仍会引发一次多余的重载，因此保留一段忽略窗口。
const writeIgnoreWindow = 1500 * time.Millisecond

// writingUntil 配置写入保护截止时间（UnixNano）。
// 在写入期间及写入完成后的忽略窗口内，热重载回调应跳过。
var writingUntil atomic.Int64

// GetConfigWriteMutex 返回全局配置写锁，供其他包使用
func GetConfigWriteMutex() *sync.Mutex {
	return &configWriteMu
}

// MarkConfigWriteStart 写配置前调用，立即进入写入保护期，暂停热重载。
func MarkConfigWriteStart() {
	writingUntil.Store(time.Now().Add(writeIgnoreWindow).UnixNano())
}

// MarkConfigWriteEnd 写配置完成后调用，延长忽略窗口，
// 覆盖 fsnotify 事件异步送达（写入结束标志复位后事件才到达）导致的竞态。
func MarkConfigWriteEnd() {
	writingUntil.Store(time.Now().Add(writeIgnoreWindow).UnixNano())
}

// IsWritingInProgress 检查是否正在写入配置（或处于写入后的忽略窗口）
func IsWritingInProgress() bool {
	return time.Now().UnixNano() < writingUntil.Load()
}

// WriteConfigKeyValue safely updates a single key in application.yaml.
// The key uses dot notation (e.g. "weibo.alertGroupId").
// It performs line-by-line text manipulation to preserve all other content.
// Uses atomic write (temp file + rename) to prevent corruption.
func WriteConfigKeyValue(key, value string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	// 标记正在写入，暂停热重载
	MarkConfigWriteStart()
	defer MarkConfigWriteEnd()

	cfgFile := config.GlobalConfig.ConfigFileUsed()
	if cfgFile == "" {
		cfgFile = "application.yaml"
	}

	return writeConfigKeyValueToPath(key, value, cfgFile)
}

// writeConfigKeyValueToPath writes a config key-value pair to the specified file path.
// Must be called with configWriteMu held.
func writeConfigKeyValueToPath(key, value, cfgPath string) error {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}

	// 保留原文件权限
	var fileMode os.FileMode = 0o644
	if info, err := os.Stat(cfgPath); err == nil {
		fileMode = info.Mode()
	}

	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid config key format: %s (expected section.name)", key)
	}
	section := parts[0]
	keyName := parts[1]

	content := string(data)
	lines := strings.Split(content, "\n")

	var out []string
	inSection := false
	indentSection := ""
	inserted := false
	keyRe := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(keyName) + `:\s*`)

	for i, line := range lines {
		trim := strings.TrimSpace(line)

		// Detect section header (e.g. "weibo:")
		if strings.HasPrefix(trim, section+":") && !inSection {
			inSection = true
			idx := strings.Index(line, section+":")
			indentSection = line[:idx]
			out = append(out, line)
			continue
		}

		if inSection {
			// Exit section: non-indented non-empty line
			if len(trim) > 0 && !strings.HasPrefix(line, indentSection+" ") && !strings.HasPrefix(line, indentSection+"\t") {
				// Insert before exiting if not yet inserted
				if !inserted {
					out = append(out, fmt.Sprintf("%s  %s: %s", indentSection, keyName, value))
					inserted = true
				}
				inSection = false
			} else {
				// Inside section: check for existing key
				if m := keyRe.FindStringSubmatch(line); m != nil {
					out = append(out, fmt.Sprintf("%s  %s: %s", indentSection, keyName, value))
					inserted = true
					continue
				}
			}
		}

		out = append(out, line)

		// Last line: still in section and not inserted
		if i == len(lines)-1 && inSection && !inserted {
			out = append(out, fmt.Sprintf("%s  %s: %s", indentSection, keyName, value))
			inserted = true
		}
	}

	// Section not found: append new section
	if !inserted {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, section+":")
		out = append(out, fmt.Sprintf("  %s: %s", keyName, value))
	}

	newData := []byte(strings.Join(out, "\n"))

	// 使用临时文件 + 原子替换，避免写入中途崩溃导致配置损坏。
	// Windows 上目标文件可能被编辑器/杀毒软件瞬时占用，rename 会失败，
	// 因此做几次退避重试，重试仍失败才返回错误（不会损坏原文件）。
	tmpFile := cfgPath + ".tmp"
	if err := os.WriteFile(tmpFile, newData, fileMode); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	const maxRenameRetries = 3
	var renameErr error
	for attempt := 0; attempt < maxRenameRetries; attempt++ {
		renameErr = os.Rename(tmpFile, cfgPath)
		if renameErr == nil {
			return nil
		}
		// 短暂退避后重试（200ms 起，最多约 800ms）
		time.Sleep(time.Duration(200*(1<<attempt)) * time.Millisecond)
	}
	_ = os.Remove(tmpFile)
	return fmt.Errorf("原子替换配置文件失败（已重试 %d 次）: %w", maxRenameRetries, renameErr)
}
