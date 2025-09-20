package acfun

import (
	"bytes"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const PathApiIsFollowing = "/rest/app/relation/isFollowing"

func GetIsFollowing(id int64) (*RelationStatusResponse, error) {
	if !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	var resp bytes.Buffer
	var respHeader requests.RespHeader
	url := APath(PathApiIsFollowing)
	opts := GetGeneralOptions(true)
	opts = append(opts, GetEncodeOption())
	params := gout.H{
		"toUserIds": id,
	}
	err := requests.GetWithHeader(url, params, &respHeader, &resp, opts...)
	if err != nil {
		logger.WithField("FuncName", utils.FuncName()).
			WithField("uid", id).Errorf("请求关注状态失败：%v", err)
		return nil, err
	}

	body, err := utils.ParseRespBody(resp, respHeader)
	if err != nil {
		return nil, err
	}

	result := new(RelationStatusResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, result)

	return result, err
}
