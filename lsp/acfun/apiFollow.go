package acfun

import (
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"time"
)

const (
	PathApiFollow = "/rest/app/relation/follow"
	ActSub        = iota
	ActUnsub
)

func SetFollow(id int64, action int) (*FollowResponse, error) {
	if !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}

	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	resp := new(FollowResponse)
	url := APath(PathApiFollow)
	opts := GetGeneralOptions(true)
	formReq := map[string]interface{}{
		"action":   action,
		"toUserId": id,
	}

	form, err := utils.ToParams(formReq)
	if err != nil {
		logger.Errorf("ToParams error %v", err)
		return nil, err
	}

	err = requests.PostForm(url, form, &resp, opts...)

	return resp, err
}
