package logging

import (
	"fmt"
	"os"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/logging/style"
)

// BannerInfo 是版本信息，供 --version 和启动日志使用。
type BannerInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// bannerArt 是启动时打印的 DDBOT 字符画（figlet standard 风格），纯 ASCII。
// 中文宽字符在不同 Windows 控制台上的列宽不一致，混进字符画里整块就会错位，所以这里一个都不放。
var bannerArt = []string{
	` ____  ____  ____   ___ _____`,
	`|  _ \|  _ \| __ ) / _ \_   _|`,
	`| | | | | | |  _ \| | | || |  `,
	`| |_| | |_| | |_) | |_| || |  `,
	`|____/|____/|____/ \___/ |_|  `,
}

// PrintBanner 打印启动字符画。
// 字符画是唯一不套日志格式的输出：它本身就是版本标记，套上时间戳和级别反而累赘。
func PrintBanner() {
	fmt.Print(renderBanner(style.Terminal(os.Stdout)))
}

// PrintVersion 打印 --version 的内容。
// 格式与改造前逐字一致（Tags / COMMIT_ID / BUILD_TIME 三行、无前缀无缩进、无颜色），
// 老脚本里 grep '^Tags:' 之类的取法不会失效。
func PrintVersion(info BannerInfo) {
	fmt.Print(renderVersion(info))
}

// VersionLine 把版本三件套拼成一行，缺失项自动省略，供启动日志使用。
func VersionLine(info BannerInfo) string {
	var parts []string
	if v := usableVersionValue(info.Version); v != "" {
		parts = append(parts, "版本 "+v)
	}
	if c := usableVersionValue(info.Commit); c != "" {
		parts = append(parts, shortCommit(c))
	}
	if t := usableVersionValue(info.BuildTime); t != "" {
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return "版本未标注"
	}
	return strings.Join(parts, " · ")
}

// renderBanner 生成字符画文本。color 为 false 时输出纯文本，供重定向和测试使用。
func renderBanner(color bool) string {
	var b strings.Builder
	for _, line := range bannerArt {
		b.WriteString(style.Paint(color, style.Cyan, strings.TrimRight(line, " ")))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// renderVersion 生成 --version 的文本，保持改造前的裸格式。
func renderVersion(info BannerInfo) string {
	return "Tags: " + orUnknown(info.Version) + "\n" +
		"COMMIT_ID: " + orUnknown(info.Commit) + "\n" +
		"BUILD_TIME: " + orUnknown(info.BuildTime) + "\n"
}

// orUnknown 把空值统一成 UNKNOWN，保持 --version 输出的字段完整。
func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "UNKNOWN"
	}
	return v
}

// usableVersionValue 过滤掉空值和 "UNKNOWN"，这两种在启动日志里不显示。
func usableVersionValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "UNKNOWN") {
		return ""
	}
	return v
}

// shortCommit 把 commit hash 截到 7 位，够认人又不占地方。
func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
