package douyin

import (
	"bytes"
	"errors"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathWebcastUserProfile = "/webcast/user/profile/"

func GetUserInfo(secId string) (*UserInfo, error) {
	profile, err := GetUserProfile(secId)
	if err != nil {
		return nil, err
	}
	result := &UserInfo{
		Uid:       profile.GetData().GetUserData().GetIdStr(),
		SecUid:    profile.GetData().GetUserData().GetSecUid(),
		Gender:    profile.GetData().GetUserData().GetGender(),
		AvatarUrl: profile.GetData().GetUserData().GetAvatarMedium().GetUrlList()[0],
		NikeName:  profile.GetData().GetUserData().GetNickname(),
		DisplayId: profile.GetData().GetUserData().GetDisplayId(),
		Signature: profile.GetData().GetUserData().GetSignature(),
		WebRoomId: profile.GetData().GetUserData().GetWebRid(),
	}
	return result, nil
}

func GetUserProfile(secId string) (*ProfileResponse, error) {
	if secId == "" {
		logger.WithField("secId", secId).
			Error("secId 不能为空")
		return nil, errors.New("secId 不能为空")
	}
	Url := DPath(PathWebcastUserProfile)
	param := make(map[string]string)
	param["aid"] = "6383"
	param["sec_target_uid"] = secId
	param["sec_anchor_id"] = secId
	opts := SetRequestOptions()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, param, &resp, &respHeaders, opts...); err != nil {
		logger.WithField("secId", secId).Errorf("获取用户信息失败：%v", err)
		return nil, err
	}

	// 解压缩HTML
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		logger.WithField("secId", secId).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}

	userProfile := new(ProfileResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, userProfile)
	if err != nil {
		logger.WithField("secId", secId).Errorf("解析用户信息失败：%v", err)
		return nil, err
	}
	if userProfile.StatusCode != 0 {
		logger.WithField("secId", secId).
			WithField("status_code", userProfile.StatusCode).
			Errorf("解析用户信息失败：%v", err)
		return nil, errors.New("数据为空")
	}
	return userProfile, nil
}
