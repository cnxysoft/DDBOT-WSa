package bilibili

import (
	"io"
	"net/http/cookiejar"
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"go.uber.org/atomic"
)

const (
	PathXSpaceAccInfo = "/x/space/wbi/acc/info"
)

type XSpaceAccInfoRequest struct {
	Mid           int64  `json:"mid"`
	Platform      string `json:"platform"`
	Token         string `json:"token"`
	WebLocation   string `json:"web_location"`
	DmImgList     string `json:"dm_img_list"`
	DmImgInter    string `json:"dm_img_inter"`
	DmCoverImgStr string `json:"dm_cover_img_str"`
	DmImgStr      string `json:"dm_img_str"`
}

var cj atomic.Pointer[cookiejar.Jar]

func refreshCookieJar() {
	j, _ := cookiejar.New(nil)
	err := requests.Get("https://www.bilibili.com/", nil, io.Discard,
		requests.WithCookieJar(j),
		AddUAOption(),
		requests.RequestAutoHostOption(),
		requests.HeaderOption("accept", "application/json"),
		requests.HeaderOption("accept-language", "zh-CN,zh;q=0.9"),
	)
	if err != nil {
		logger.Errorf("bilibili: refreshCookieJar request error %v", err)
	}
	cj.Store(j)
}

func XSpaceAccInfo(mid int64) (*XSpaceAccInfoResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	const headerOrigin = "https://space.bilibili.com"
	const headerAcceptLanguage = "zh-CN,zh;q=0.9"
	//const headerUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	//const headerBuvId3 = "55F2E775-090F-EC53-DDED-6FDD87687DAA76234infoc"
	//const headerBNut = "1716350676"
	const headerBLsid = "5641F797_18F9E78EF04"
	const headerUuid = "F1BD8FB10-9651-10596-DA37-1CEC9A469A5376746infoc"

	accInfoUrl := BPath(PathXSpaceAccInfo)
	params, err := utils.ToDatas(&XSpaceAccInfoRequest{
		Mid:           mid,
		DmImgList:     "",
		Platform:      "web",
		WebLocation:   "1550101",
		DmImgStr:      "V2ViR0wgMS4wIChPcGVuR0wgRVMgMi4wIENocm9taXVtKQ",
		DmCoverImgStr: "QU5HTEUgKE5WSURJQSwgTlZJRElBIEdlRm9yY2UgUlRYIDMwOTAgKDB4MDAwMDIyMDQpIERpcmVjdDNEMTEgdnNfNV8wIHBzXzVfMCwgRDNEMTEpR29vZ2xlIEluYy4gKE5WSURJQS",
		DmImgInter:    `{"ds":[],"wh":[0,0,0],"of":[0,0,0]}`,
	})
	if err != nil {
		return nil, err
	}
	signWbi(params)
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 15),
		AddUAOption(),
		requests.HeaderOption("Accept", "application/json"),
		requests.HeaderOption("Accept-language", headerAcceptLanguage),
		requests.HeaderOption("Origin", headerOrigin),
		requests.HeaderOption("Referer", headerOrigin+"/"+strconv.FormatInt(mid, 10)),
		//requests.HeaderOption("User-Agent", headerUserAgent),
		//requests.CookieOption("buvid3", headerBuvId3),
		//requests.CookieOption("b_nut", headerBNut),
		requests.CookieOption("b_lsid", headerBLsid),
		requests.CookieOption("_uuid", headerUuid),
		requests.RequestAutoHostOption(),
		requests.WithCookieJar(cj.Load()),
		requests.NotIgnoreEmptyOption(),
		delete412ProxyOption,
	}
	opts = append(opts, GetVerifyOption()...)
	xsai := new(XSpaceAccInfoResponse)
	err = requests.Get(accInfoUrl, params, xsai, opts...)
	if err != nil {
		return nil, err
	}
	return xsai, nil
}
