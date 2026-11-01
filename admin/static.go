package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// webFS 内嵌 Web 管理面板的静态资源（admin/web/ 目录）。
// 保持单二进制部署形态，无需随包分发前端文件。
//
//go:embed all:web
var webFS embed.FS

// serveWebFile 提供 Web 面板静态资源。
//
// 静态资源本身不经过 withAuth：页面需要先加载才能展示登录入口，
// 且所有数据接口都注册在 withAuth 之后，不暴露任何敏感信息。
// 未知路径回退到 index.html，配合前端 hash 路由（#/page）支持直接访问与刷新。
func serveWebFile(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		http.Error(w, "web ui unavailable", http.StatusInternalServerError)
		return
	}

	// path.Clean 归一化路径；embed.FS 自身会拒绝目录穿越，这里再做一层归一
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if _, err := fs.Stat(sub, name); err != nil {
		// SPA 兜底：未命中静态文件的路径返回首页，由前端路由接管
		name = "index.html"
	}

	// 内嵌资源没有可靠的 mtime/ETag，no-cache 在无验证器时浏览器仍可能沿用缓存
	// （实测 Chromium 完整导航也命中旧 JS），管理后台页面直接 no-store 每次全量拉取。
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFileFS(w, r, sub, name)
}
