package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/samber/lo"
)

const PathXWebInterfaceNav = "/x/web-interface/nav"

func refreshNavWbi() {
	resp, err := XWebInterfaceNav(false)
	if err != nil {
		logger.Errorf("bilibili: refreshNavWbi error %v", err)
		return
	}
	wbiImg := resp.GetData().GetWbiImg()
	if wbiImg != nil {
		wbi.Store(wbiImg)
	}
	logger.Trace("bilibili: refreshNavWbi ok")
}

func getWbi() (imgKey string, subKey string) {
	wbi := wbi.Load()
	getKey := func(url string) string {
		path, _ := lo.Last(strings.Split(url, "/"))
		key := strings.Split(path, ".")[0]
		return key
	}
	imgKey = getKey(wbi.ImgUrl)
	subKey = getKey(wbi.SubUrl)
	return
}
func getMixinKey(orig string) string {
	var str strings.Builder
	for _, v := range mixinKeyEncTab {
		if v < len(orig) {
			str.WriteByte(orig[v])
		}
	}
	return str.String()[:32]
}

func signWbi(params map[string]string) map[string]string {
	imgKey, subKey := getWbi()
	mixinKey := getMixinKey(imgKey + subKey)
	currTime := strconv.FormatInt(time.Now().Unix(), 10)
	params["wts"] = currTime
	urlParams := url.Values{}
	for key, value := range params {
		urlParams.Add(key, fmt.Sprintf("%v", value))
	}
	query := urlParams.Encode()
	hash := md5.Sum([]byte(query + mixinKey))
	params["w_rid"] = hex.EncodeToString(hash[:])
	return params
}

func XWebInterfaceNav(login bool) (*WebInterfaceNavResponse, error) {
	if login && !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	path := BPath(PathXWebInterfaceNav)
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 15),
		AddUAOption(),
		delete412ProxyOption,
	}
	if login && getVerify() != nil {
		opts = append(opts, getVerify().VerifyOpts...)
	}
	xwin := new(WebInterfaceNavResponse)
	err := requests.Get(path, nil, xwin, opts...)
	if err != nil {
		return nil, err
	}
	return xwin, nil
}
