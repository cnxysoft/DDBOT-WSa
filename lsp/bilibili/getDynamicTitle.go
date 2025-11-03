package bilibili

import (
	"bytes"
	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

func reqDynamicPage(DynamicId string) string {
	Url := DynamicUrl(DynamicId)
	opt := []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
		requests.RequestAutoHostOption(),
	}
	var resp bytes.Buffer
	err := requests.Get(Url, nil, &resp, opt...)
	if err != nil {
		logger.Warnf("获取动态页面失败: %v", err)
		return ""
	}
	return getDynamicTitle(resp.Bytes())
}

func getDynamicTitle(data []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		logger.Warnf("解析动态页面失败: %v", err)
		return ""
	}
	title := doc.Find(".opus-module-title__text").Text()
	return title
}
