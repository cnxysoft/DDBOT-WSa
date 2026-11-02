package cfg

import "strings"

// isSectionHeader 判断这一行是不是**顶层**的 section: 段头。
//
// 三个条件都不能少：
//   - 顶格。缩进的同名键属于别的段（platform.weibo），当成段头会把值写错地方；
//   - 大小写不敏感，且容忍 "weibo": 这种引号写法 —— viper 就是大小写不敏感的，
//     认不出来就会在文件末尾追加第二个同名段，重复顶层键让 yaml.v3 直接报错；
//   - 冒号后面只能是空值或注释，避免把 weibo.something: 误认成段头。
func isSectionHeader(line, section string) bool {
	if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
		strings.HasPrefix(line, "#") {
		return false
	}
	rest := strings.TrimSpace(line)
	if rest == "" {
		return false
	}
	if q := rest[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(rest[1:], q)
		if end < 0 || !strings.EqualFold(rest[1:1+end], section) {
			return false
		}
		rest = strings.TrimSpace(rest[2+end:])
		// 引号后面还得是冒号；只是个字符串（不是段头）的情况不能认
		if !strings.HasPrefix(rest, ":") {
			return false
		}
		rest = rest[1:]
	} else {
		idx := strings.IndexByte(rest, ':')
		if idx < 0 || !strings.EqualFold(rest[:idx], section) {
			return false
		}
		rest = strings.TrimSpace(rest[idx+1:])
	}
	return rest == "" || strings.HasPrefix(rest, "#")
}

// keepInlineComment 从原来的「值」里捞出末尾的行内注释。
// 官方文档的示例就写着 SESSDATA: # 你的b站cookie，整段丢掉会把用户的注释吃掉。
// 带引号的值里 # 可能是值的一部分，那种情况不猜。
func keepInlineComment(oldValue string) string {
	v := strings.TrimRight(oldValue, " \t")
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") {
		return ""
	}
	if strings.HasPrefix(v, "#") {
		return " " + v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		return " " + v[i+1:]
	}
	return ""
}

// WriteConfigKeyValueToPath 是 WriteConfigKeyValue 的显式路径版本：不碰全局配置、
// 不持锁（串行由调用方负责）。微博回写 SUB 复用它，避免两份实现各自漂移。
func WriteConfigKeyValueToPath(key, value, cfgPath string) error {
	return writeConfigKeyValueToPath(key, value, cfgPath)
}
