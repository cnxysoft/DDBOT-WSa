package lolicon_pool

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"net/http"
	"net/url"
	"time"
)

const Host = "https://api.lolicon.app/setu"

type R18Type int

const (
	R18Off R18Type = iota
	R18On
	//R18Mix
)

func (r R18Type) String() string {
	switch r {
	case R18Off:
		return "R18Off"
	case R18On:
		return "R18On"
	//case R18Mix:
	//	return "R18Mix"
	default:
		return "Unknown"
	}
}

type Request struct {
	Apikey   string `json:"apikey"`
	R18      int    `json:"r18"`
	Keyword  string `json:"keyword"`
	Num      int    `json:"num"`
	Proxy    string `json:"proxy"`
	Size1200 bool   `json:"size1200"`
}

type Setu struct {
	Pid    int      `json:"pid"`
	P      int      `json:"p"`
	Uid    int      `json:"uid"`
	Title  string   `json:"title"`
	Author string   `json:"author"`
	Url    string   `json:"url"`
	R18    bool     `json:"r18"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Tags   []string `json:"tags"`
}

func (s *Setu) Content() ([]byte, error) {
	return utils.ImageGet(s.Url, requests.HeaderOption("referer", "https://www.pixiv.net"), requests.ProxyOption(proxy_pool.PreferOversea))
}

type Response struct {
	Code        int     `json:"code"`
	Msg         string  `json:"msg"`
	Quota       int     `json:"quota"`
	QuotaMinTTL int     `json:"quota_min_ttl"`
	Count       int     `json:"count"`
	Data        []*Setu `json:"data"`
}

func LoliconAppSetu(apikey string, R18 R18Type, keyword string, num int) (*Response, error) {
	return LoliconAppSetuContext(context.Background(), apikey, R18, keyword, num)
}

func LoliconAppSetuContext(ctx context.Context, apikey string, R18 R18Type, keyword string, num int) (*Response, error) {
	params, err := utils.ToParams(&Request{
		Apikey:   apikey,
		R18:      int(R18),
		Keyword:  keyword,
		Num:      num,
		Proxy:    config.GlobalConfig.GetString("loliconPool.proxy"),
		Size1200: true,
	})
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	for key, value := range params {
		query.Set(key, fmt.Sprint(value))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(*http.Request) (*url.URL, error) {
		if proxy, err := proxy_pool.Get(proxy_pool.PreferOversea); err == nil && proxy != nil {
			return url.Parse(proxy.ProxyString())
		}
		return nil, nil
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Host+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image API HTTP status %d", response.StatusCode)
	}
	apiResp := new(Response)
	if err = stdjson.NewDecoder(response.Body).Decode(apiResp); err != nil {
		return nil, err
	}
	return apiResp, nil
}
