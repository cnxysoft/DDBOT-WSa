package youtube

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"sync"
)

type UserInfo struct {
	ChannelId   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
}

func (ui *UserInfo) GetChannelName() string {
	if ui == nil {
		return ""
	}
	return ui.ChannelName
}

const (
	Video concern_type.Type = "news"
	Live  concern_type.Type = "live"
)

// VideoInfo may be a video or a live, depend on the VideoType
type VideoInfo struct {
	UserInfo
	Cover          string      `json:"cover"`
	VideoId        string      `json:"video_id"`
	VideoTitle     string      `json:"video_title"`
	VideoType      VideoType   `json:"video_type"`
	VideoStatus    VideoStatus `json:"video_status"`
	VideoTimestamp int64       `json:"video_timestamp"`

	once              sync.Once
	msgCache          *mmsg.MSG
	liveStatusChanged bool
	liveTitleChanged  bool
}

func (v *VideoInfo) TitleChanged() bool {
	return v.liveTitleChanged
}

func (v *VideoInfo) Living() bool {
	return v.IsLiving()
}

func (v *VideoInfo) LiveStatusChanged() bool {
	return v.liveStatusChanged
}

func (v *VideoInfo) Site() string {
	return Site
}

func (v *VideoInfo) GetUid() interface{} {
	return v.ChannelId
}

func (v *VideoInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":        Site,
		"ChannelId":   v.ChannelId,
		"ChannelName": v.ChannelName,
		"VideoId":     v.VideoId,
		"VideoType":   v.VideoType.String(),
		"VideoTitle":  v.VideoTitle,
		"VideoStatus": v.VideoStatus.String(),
	})
}

func (v *VideoInfo) Type() concern_type.Type {
	if v.IsLive() {
		return Live
	} else {
		return Video
	}
}

func (v *VideoInfo) IsLive() bool {
	if v == nil {
		return false
	}
	return v.VideoType == VideoType_FirstLive || v.VideoType == VideoType_Live
}

func (v *VideoInfo) IsLiving() bool {
	if v == nil {
		return false
	}
	return v.IsLive() && v.VideoStatus == VideoStatus_Living
}

func (v *VideoInfo) IsWaiting() bool {
	if v == nil {
		return false
	}
	return v.IsLive() && v.VideoStatus == VideoStatus_Waiting
}

func (v *VideoInfo) IsVideo() bool {
	if v == nil {
		return false
	}
	return v.VideoType == VideoType_Video
}

func (v *VideoInfo) GetMSG() *mmsg.MSG {
	v.once.Do(func() {
		m := mmsg.NewMSG()
		if v.IsLive() {
			if v.IsLiving() {
				m.Textf("YTB-%v正在直播：\n%v\n", v.ChannelName, v.VideoTitle)
			} else {
				m.Textf("YTB-%v发布了直播预约：\n%v\n时间：%v\n",
					v.ChannelName, v.VideoTitle, localutils.TimestampFormat(v.VideoTimestamp))
			}
		} else if v.IsVideo() {
			m.Textf("YTB-%s发布了新视频：\n%v\n", v.ChannelName, v.VideoTitle)
		}
		m.ImageByUrl(v.Cover, "[封面]", requests.ProxyOption(proxy_pool.PreferOversea))
		m.Text(VideoViewUrl(v.VideoId) + "\n")
		v.msgCache = m
	})
	return v.msgCache
}

type Info struct {
	VideoInfo []*VideoInfo `json:"video_info"`
	UserInfo
}

func (i *Info) ToString() string {
	if i == nil {
		return ""
	}
	b, _ := json.Marshal(i)
	return string(b)
}

func NewInfo(vinfo []*VideoInfo, addMode bool) *Info {
	info := new(Info)
	if !addMode {
		info.VideoInfo = vinfo
	}
	if len(vinfo) > 0 {
		info.ChannelId = vinfo[0].ChannelId
		info.ChannelName = vinfo[0].ChannelName
	}
	return info
}

type ConcernNotify struct {
	*VideoInfo
	GroupCode int64 `json:"group_code"`
}

func (notify *ConcernNotify) GetGroupCode() int64 {
	return notify.GroupCode
}

func (notify *ConcernNotify) ToMessage() (m *mmsg.MSG) {
	return notify.VideoInfo.GetMSG()
}

func (notify *ConcernNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return notify.VideoInfo.Logger().WithFields(localutils.GroupLogFields(notify.GroupCode))
}

func NewConcernNotify(groupCode int64, info *VideoInfo) *ConcernNotify {
	if info == nil {
		return nil
	}
	return &ConcernNotify{
		VideoInfo: info,
		GroupCode: groupCode,
	}
}

func NewUserInfo(channelId, channelName string) *UserInfo {
	return &UserInfo{
		ChannelId:   channelId,
		ChannelName: channelName,
	}
}
