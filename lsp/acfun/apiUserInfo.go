package acfun

import (
	"bytes"
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const PathApiUserInfo = "/rest/app/user/userInfo"

func GetUserInfo(id int64) (*SpaceProfileResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	var resp bytes.Buffer
	var respHeader requests.RespHeader
	url := APath(PathApiUserInfo)
	opts := GetGeneralOptions(true)
	opts = append(opts, GetEncodeOption())
	params := gout.H{"userId": id}

	err := requests.GetWithHeader(url, params, &resp, &respHeader, opts...)
	if err != nil {
		return nil, err
	}

	body, err := utils.ParseRespBody(resp, respHeader)
	if err != nil {
		return nil, err
	}

	result := new(SpaceProfileResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, result)
	if err != nil {
		return nil, err
	}
	if result.GetResult() != 0 {
		return nil, fmt.Errorf("获取用户信息失败：%v - %v", result.GetResult(), result.GetErrorMsg())
	}
	return result, nil
}
