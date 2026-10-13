package acfun

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"regexp"
	"time"
)

var livePageRegex = regexp.MustCompile("<script>window.__INITIAL_STATE__=(?P<json>.*?);\\(")

func LivePage(uid int64) (*LivePageResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	url := LiveUrl(uid)
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second*10),
	)
	var body = new(bytes.Buffer)
	err := requests.Get(url, nil, body, opts...)
	if err != nil {
		return nil, err
	}
	var b = body.Bytes()

	match := livePageRegex.FindSubmatch(b)
	if len(match) <= livePageRegex.SubexpIndex("json") {
		return nil, errors.New("no json data matched")
	}
	var result = new(LivePageResponse)
	if err = json.Unmarshal(match[livePageRegex.SubexpIndex("json")], result); err != nil {
		return nil, err
	}
	if len(result.GetLiveInfo().GetUser().GetName()) == 0 {
		return nil, fmt.Errorf("live page 未包含用户信息，uid %d 可能不存在", uid)
	}
	return result, nil
}
