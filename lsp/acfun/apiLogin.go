package acfun

import (
	"errors"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

const PathApiLogin = "/rest/app/login/signin"

func Login(username string, password string) (*LoginResponse, error) {
	if len(username) == 0 {
		return nil, errors.New("empty username")
	}
	if len(password) == 0 {
		return nil, errors.New("empty password")
	}

	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.Tracef("cost %v", ed.Sub(st))
	}()

	url := APath(PathApiLogin)
	opts := GetGeneralOptions(false)
	opts = append(opts, []requests.Option{
		requests.HeaderOption("devicetype", "1"),
		requests.HeaderOption("appversion", "6.77.0.1306"),
	}...)
	formReq := map[string]interface{}{
		"password": password,
		"username": username,
	}

	form, err := utils.ToParams(formReq)
	if err != nil {
		logger.Errorf("ToParams error %v", err)
		return nil, err
	}

	var loginResp = new(LoginResponse)
	err = requests.PostWWWForm(url, form, &loginResp, opts...)

	return loginResp, err
}
