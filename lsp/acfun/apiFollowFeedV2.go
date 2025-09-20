package acfun

import (
	"bytes"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const PathApiFollowFeedV2 = "/rest/app/feed/followFeedV2"

func GetFollowFeedV2() (*FollowFeedV2Response, error) {
	if !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	var (
		resp       bytes.Buffer
		respHeader requests.RespHeader
		params     gout.H
	)

	url := APath(PathApiFollowFeedV2)
	opts := GetGeneralOptions(true)
	opts = append(opts, GetEncodeOption())
	params = gout.H{
		"count": 20,
	}

	err := requests.GetWithHeader(url, params, &resp, &respHeader, opts...)
	if err != nil {
		return nil, err
	}

	body, err := utils.ParseRespBody(resp, respHeader)
	if err != nil {
		return nil, err
	}

	result := new(FollowFeedV2Response)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, result)
	if err != nil {
		return nil, err
	}

	return result, nil
}
