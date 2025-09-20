package acfun

import (
	"bytes"
	"errors"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathApiGetFollows = "/rest/app/relation/getFollows"

func GetFollows(page int) (*FollowsResponse, error) {
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

	url := APath(PathApiGetFollows)
	opts := GetGeneralOptions(true)
	opts = append(opts, GetEncodeOption())
	if page == 0 {
		page = 1
	}
	params = gout.H{
		"action": 7,
		"page":   page,
		"count":  20,
	}

	err := requests.GetWithHeader(url, params, &resp, &respHeader, opts...)
	if err != nil {
		return nil, err
	}

	body, err := utils.ParseRespBody(resp, respHeader)
	if err != nil {
		return nil, err
	}

	result := new(FollowsResponse)
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

func GetAttentionList() ([]*Follow, error) {
	var followList []*Follow
	page := 1

	for {
		resp, err := GetFollows(page)
		if err != nil {
			logger.WithField("FuncName", utils.FuncName()).WithField("UserId", username).Errorf("获取关注列表失败：%v", err)
			return nil, err
		}
		if resp.GetResult() != 0 {
			logger.WithField("code", resp.GetResult()).
				WithField("msg", resp.GetErrorMsg()).
				Errorf("SyncSub GetAttentionList error")
			return nil, errors.New("获取关注列表失败")
		}

		followList = append(followList, resp.GetFriendList()...)
		if resp.GetPcursor() == "no_more" {
			break
		}
		page += 1
	}

	return followList, nil
}
