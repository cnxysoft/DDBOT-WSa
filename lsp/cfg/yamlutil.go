package cfg

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

	cfgFile := ConfigFilePath()

	return writeConfigKeyValueToPath(key, value, cfgFile)
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

	// 整行匹配：缩进 + 键名 + 冒号前空格 + 冒号 + 冒号后空格 + 值。
	// 命中时按原样拼回、只换值——缩进、键名大小写、冒号两侧的写法都不动。
	// 匹配必须大小写不敏感（viper 就是大小写不敏感）：文件里写的是小写 sessdata 时
	// 若匹配不上，就会在段尾另起一行 SESSDATA，同一份配置出现两个等价键。
	keyRe := regexp.MustCompile(`^(\s*)("?)([A-Za-z0-9_.\-]+)("?)(\s*):(\s*)(.*)$`)
	quoted := quoteYAMLValue(value)

	// Windows 上编辑过的配置是 CRLF。上面那个正则把行尾的 \r 一起吃进了第 7 组，
	// 拼回时如果不补上，这两行就从 CRLF 变成 LF —— 文件成了混合换行。
	// YAML 仍合法，但 diff 会整段炸、编辑器显示也会异常。群友反馈的「排版被改」，
	// 除了缩进和键名大小写，也包括这个。
	eol := ""
	if strings.Contains(string(data), "\r\n") {
		eol = "\r"
	}

	lines := strings.Split(string(data), "\n")

	var out []string
	inSection := false
	indentSection := ""
	childIndent := "" // 段内已有子键的缩进，插入新键时对齐它
	inserted := false

	// 段里已有子键就照它的缩进插，没有才退回段头缩进 + 两空格。
	// 写死两空格会把 4 空格缩进的段改成混合缩进，YAML 直接不合法。
	insertKey := func() string {
		indent := childIndent
		if indent == "" {
			indent = indentSection + "  "
		}
		return indent + keyName + ": " + quoted + eol
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)

		// 段头（如 "bilibili:"）
		if !inSection && strings.HasPrefix(trim, section+":") {
			inSection = true
			indentSection = line[:strings.Index(line, section+":")]
			out = append(out, line)
			continue
		}

		if inSection {
			// 段外：非空行且不比段头更深
			if len(trim) > 0 && !strings.HasPrefix(line, indentSection+" ") && !strings.HasPrefix(line, indentSection+"\t") {
				if !inserted {
					out = append(out, insertKey())
					inserted = true
				}
				inSection = false
			} else if m := keyRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[3], keyName) {
				out = append(out, m[1]+m[2]+m[3]+m[4]+m[5]+":"+m[6]+quoted+eol)
				inserted = true
				continue
			} else if childIndent == "" && len(trim) > 0 && !strings.HasPrefix(trim, "#") {
				if idx := strings.Index(line, trim); idx > len(indentSection) {
					childIndent = line[:idx]
				}
			}
		}

		out = append(out, line)
	}

	// 段一直延伸到文件末尾且没找到该键：插到段内最后一个非空行之后（跳过尾部空行，
	// 否则新键会落在空行后面，虽然仍是合法 YAML 但看着别扭）
	if inSection && !inserted {
		pos := len(out)
		for pos > 0 && strings.TrimSpace(out[pos-1]) == "" {
			pos--
		}
		out = append(out, "")
		copy(out[pos+1:], out[pos:])
		out[pos] = insertKey()
		inserted = true
	}

	// 没有该段：追加一个新段
	if !inserted {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, section+":"+eol)
		out = append(out, "  "+keyName+": "+quoted+eol)
	}

	// 追加新段时 out 的末尾会少一个空元素，这里补回原文件的结尾换行约定
	joined := strings.Join(out, "\n")
	if strings.HasSuffix(string(data), "\n") && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	newData := []byte(joined)

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
