package bilibili

import (
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"strconv"
	"time"
)

const (
	PathRoomInfo = "/room/v1/Room/get_info"
)

type RoomInfoRequest struct {
	RoomId int64 `json:"room_id"`
}

func GetRoomInfo(roomId int64) (*LiveRoomData, error) {
	params, err := utils.ToDatas(&RoomInfoRequest{
		RoomId: roomId,
	})
	if err != nil {
		return nil, err
	}
	opts := []requests.Option{
		AddUAOption(),
		requests.TimeoutOption(time.Second * 15),
		requests.RequestAutoHostOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.WithCookieJar(cj.Load()),
	}
	opts = append(opts, GetVerifyOption()...)
	var resp = new(LiveRoomData)
	err = requests.Get(BPath(PathRoomInfo), params, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (l *LiveRoomData) GetLiveStatus() LiveStatus {
	if l.Data.LiveStatus == 1 {
		return LiveStatus_Living
	}
	return LiveStatus_NoLiving
}

func (l *LiveRoomData) GetCover() string {
	return l.Data.UserCover
}

func (l *LiveRoomData) GetTitle() string {
	return l.Data.Title
}

func (l *LiveRoomData) GetUrl() string {
	return "https://live.bilibili.com/" + strconv.FormatInt(l.Data.RoomId, 10)
}
