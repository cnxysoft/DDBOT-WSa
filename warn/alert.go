package warn

import (
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/logging/style"
)

// alertText 渲染带标记的阻断性提示：首行 "  [!] "，续行按同一宽度缩进，多行也能对齐。
// 纯 ASCII 标记，Windows 控制台不会错位。
func alertText(content string, color bool) string {
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, line := range lines {
		prefix := "  [!] "
		if i > 0 {
			prefix = "      "
		}
		b.WriteString(style.Paint(color, style.Bold+";"+style.Red, prefix+line))
		b.WriteString("\n")
	}
	return b.String()
}
