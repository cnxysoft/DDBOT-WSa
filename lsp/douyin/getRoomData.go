package douyin

import (
	"bytes"
	"errors"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathWebcastRoomWebEnter = "/webcast/room/web/enter/"

func GetRoomData(roomId string) (*RoomDataResponse, error) {
	Url := DPath(PathWebcastRoomWebEnter)
	param := make(map[string]string)
	param["aid"] = "6383"
	param["web_rid"] = roomId
	opts := SetRequestOptions()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, param, &resp, &respHeaders, opts...); err != nil {
		logger.WithField("roomId", roomId).Errorf("获取直播间数据失败：%v", err)
		return nil, err
	}

	// 解压缩HTML
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		logger.WithField("roomId", roomId).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}

	roomData := new(RoomDataResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, roomData)
	if err != nil || roomData.StatusCode != 0 {
		logger.WithField("roomId", roomId).Errorf("解析直播间数据失败：%v", err)
		return nil, err
	}
	if len(roomData.GetData().GetData()) < 1 {
		logger.WithField("roomId", roomId).Errorf("解析直播间数据失败：%v", err)
		return nil, errors.New("数据为空")
	}
	return roomData, nil
}
