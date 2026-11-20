package weibo

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// WeiboCookieResponse 定义从 Cookie 刷新 API 返回的响应结构
type WeiboCookieResponse struct {
	Success bool                  `json:"success"`
	Count   int                   `json:"count"`
	Cookies []WeiboCookieKeyValue `json:"cookies"`
}

// WeiboCookieKeyValue 定义单个 Cookie 的键值对
type WeiboCookieKeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FreshCookieFromAPI 从外部 API 获取新的微博 Cookie
func FreshCookieFromAPI() ([]*http.Cookie, error) {
	apiURL := cfg.GetWeiboCookieRefreshAPI()
	if apiURL == "" {
		return nil, fmt.Errorf("未配置微博 Cookie 刷新 API 地址")
	}

	var resp WeiboCookieResponse
	err := requests.Get(apiURL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("请求 Cookie 刷新 API 失败：%w", err)
	}

	// 检查响应是否有效
	if !resp.Success {
		logger.Errorf("API 返回失败，请检查 API 服务状态")
		return nil, fmt.Errorf("Cookie 刷新 API 返回失败")
	}

	if len(resp.Cookies) == 0 {
		logger.Errorf("API 返回空 Cookie 列表")
		return nil, fmt.Errorf("Cookie 刷新 API 返回空 Cookie 列表")
	}

	// 验证 Cookie 数据
	for i, ck := range resp.Cookies {
		if ck.Name == "" {
			logger.Warnf("第 %d 个 Cookie 名称为空，跳过", i)
			continue
		}
		if ck.Value == "" {
			logger.Warnf("Cookie '%s' 值为空，跳过", ck.Name)
			continue
		}
	}

	// 将 API 返回的 Cookie 转换为 http.Cookie 格式
	cookies := make([]*http.Cookie, 0, len(resp.Cookies))
	for _, ck := range resp.Cookies {
		if ck.Name == "" || ck.Value == "" {
			continue // 跳过无效的 Cookie
		}
		cookie := &http.Cookie{
			Name:    ck.Name,
			Value:   ck.Value,
			Path:    "/",
			Domain:  "weibo.com",
			Expires: time.Now().Add(1 * time.Hour),
		}
		cookies = append(cookies, cookie)
	}

	if len(cookies) == 0 {
		return nil, fmt.Errorf("没有有效的 Cookie")
	}

	logger.Infof("从 API 成功获取 %d 个微博 Cookie", len(cookies))

	// 打印所有 Cookie 用于调试
	for _, ck := range cookies {
		logger.Debugf("Cookie: %s = %s...", ck.Name, ck.Value[:min(20, len(ck.Value))])
	}

	return cookies, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ExtractSUBFromCookies 从 Cookie 列表中提取 SUB 值
func ExtractSUBFromCookies(cookies []*http.Cookie) string {
	for _, cookie := range cookies {
		if cookie.Name == "SUB" {
			return cookie.Value
		}
	}
	return ""
}
