package acfun

import (
	"bytes"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathApiPersonalInfo = "/rest/app/user/personalInfo"

func GetUserPersonalInfo() (*UserResponse, error) {
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
	)

	Url := APath(PathApiPersonalInfo)
	opts := GetGeneralOptions(true)
	opts = append(opts, GetEncodeOption())
	err := requests.GetWithHeader(Url, nil, &resp, &respHeader, opts...)
	if err != nil {
		return nil, err
	}

	body, err := utils.ParseRespBody(resp, respHeader)
	if err != nil {
		return nil, err
	}

	result := new(UserResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, result)
	if err != nil || result.GetResult() != 0 {
		logger.WithField("FuncName", utils.FuncName()).WithField("UserId", username).Errorf("解析账号信息失败：%v", err)
		return nil, err
	}

	return result, nil
}
