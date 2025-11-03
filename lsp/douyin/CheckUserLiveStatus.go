package douyin

import (
	"bytes"
	"encoding/json"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

const PathCheckUserLiveStatus = "/webcast/distribution/check_user_live_status"

type LiveStatus struct {
	StatusCode int `json:"status_code"`
	Data       []struct {
		SceneId  int `json:"scene_id"`
		UserLive []struct {
			AdType       int    `json:"ad_type"`
			FilterReason string `json:"filter_reason"`
			LiveStatus   int    `json:"live_status"`
			Msg          string `json:"msg"`
			RoomId       int64  `json:"room_id"`
			RoomIdStr    string `json:"room_id_str"`
			UserId       int64  `json:"user_id"`
			UserIdStr    string `json:"user_id_str"`
		} `json:"user_live"`
	} `json:"data"`
	Extra struct {
		Now int64 `json:"now"`
	} `json:"extra"`
}

func FreshLiveStatus(id string) (bool, error) {
	var isLive bool
	Url := DPath(PathCheckUserLiveStatus)
	param := make(map[string]string)
	param["channel"] = "channel_pc_web"
	param["user_ids"] = id
	opts := SetRequestOptions()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, param, &resp, &respHeaders, opts...); err != nil {
		logger.WithField("userId", id).Errorf("获取直播状态失败：%v", err)
		return isLive, err
	}

	// 解压缩HTML
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		logger.WithField("userId", id).Errorf("解压缩HTML失败：%v", err)
		return isLive, err
	}

	var status LiveStatus
	err = json.Unmarshal(body, &status)
	if err != nil {
		logger.WithField("userId", id).Errorf("解析直播状态信息失败：%v", err)
		return isLive, err
	}

	if len(status.Data) > 0 && len(status.Data[0].UserLive) > 0 && status.Data[0].UserLive[0].LiveStatus == 1 {
		isLive = true
	}

	return isLive, nil
}
