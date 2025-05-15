package bilibili

import (
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"time"
)

const (
	PathGetPlayTogetherUserAnchorInfoV2 = "/xlive/app-ucenter/v2/playTogether/GetPlayTogetherUserAnchorInfoV2"
)

type GetPlayTogetherUserAnchorInfoV2Request struct {
	Ruid int64 `json:"ruid"`
}

func GetPlayTogetherUserAnchorInfoV2(mid int64) (*GetPlayTogetherUserAnchorInfoV2Response, error) {
	params, err := utils.ToDatas(&GetPlayTogetherUserAnchorInfoV2Request{
		Ruid: mid,
	})
	if err != nil {
		return nil, err
	}
	opts := []requests.Option{
		AddUAOption(),
		requests.TimeoutOption(time.Second * 15),
		requests.RequestAutoHostOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.WithCookieJar(cj.Load()),
	}
	opts = append(opts, GetVerifyOption()...)
	// 查自己的话只会有anchor_nickname和anchor_avatar
	var resp = new(GetPlayTogetherUserAnchorInfoV2Response)
	err = requests.Get(BPath(PathGetPlayTogetherUserAnchorInfoV2), params, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (i *GetPlayTogetherUserAnchorInfoV2Response) GetName() string {
	return i.Data.UserAnchorInfoBase.AnchorNickname
}

func (i *GetPlayTogetherUserAnchorInfoV2Response) GetUser() *UserAnchorInfoBase {
	return i.Data.UserAnchorInfoBase
}

func (i *GetPlayTogetherUserAnchorInfoV2Response) GetUid() int64 {
	return i.Data.UserAnchorInfoBase.Uid
}
