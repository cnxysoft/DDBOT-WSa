package acfun

import (
	"errors"
	"github.com/Mrs4s/MiraiGo/message"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
	"time"
)

type StateManager struct {
	*concern.StateManager
	*ExtraKey
	concern *Concern
}

func (s *StateManager) GetGroupConcernConfig(groupCode int64, id interface{}) (concernConfig concern.IConfig) {
	return NewGroupConcernConfig(s.StateManager.GetGroupConcernConfig(groupCode, id), s.concern)
}

func NewStateManager(c *Concern) *StateManager {
	sm := &StateManager{
		concern: c,
	}
	sm.ExtraKey = NewExtraKey()
	sm.StateManager = concern.NewStateManagerWithInt64ID(Site, c.notify)
	return sm
}

func (s *StateManager) GetUserInfo(uid int64) (*UserInfo, error) {
	var userInfo *UserInfo
	err := s.GetJson(s.UserInfoKey(uid), &userInfo)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (s *StateManager) AddUserInfo(info *UserInfo) error {
	if info == nil {
		return errors.New("<nil userinfo>")
	}
	return s.SetJson(s.UserInfoKey(info.Uid), info)
}

func (s *StateManager) AddLiveInfo(info *LiveInfo) error {
	return s.RWCover(func() error {
		err := s.SetJson(s.UserInfoKey(info.Uid), info.UserInfo)
		if err != nil {
			return err
		}
		err = s.SetJson(s.LiveInfoKey(info.Uid), info)
		return err
	})
}

func (s *StateManager) GetLiveInfo(uid int64) (*LiveInfo, error) {
	var liveInfo *LiveInfo
	err := s.GetJson(s.LiveInfoKey(uid), &liveInfo)
	if err != nil {
		return nil, err
	}
	return liveInfo, nil
}

func (s *StateManager) DeleteLiveInfo(uid int64) error {
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		_, err := tx.Delete(s.LiveInfoKey(uid))
		return err
	})
}

func (s *StateManager) IncNotLiveCount(uid int64) int64 {
	result, err := s.SeqNext(s.NotLiveKey(uid))
	if err != nil {
		result = 0
	}
	return result
}

func (s *StateManager) ClearNotLiveCount(uid int64) error {
	_, err := s.Delete(s.NotLiveKey(uid), localdb.IgnoreNotFoundOpt())
	return err
}

func (s *StateManager) SetUidFirstTimestampIfNotExist(uid int64, timestamp int64) error {
	return s.SetInt64(s.UidFirstTimestamp(uid), timestamp, localdb.SetNoOverWriteOpt())
}

func (s *StateManager) GetUidFirstTimestamp(uid int64) (timestamp int64, err error) {
	timestamp, err = s.GetInt64(s.UidFirstTimestamp(uid))
	if err != nil {
		timestamp = 0
	}
	return
}

func SetCookieInfo(username string, cookieInfo *LoginResponse) error {
	if cookieInfo == nil {
		return errors.New("<nil> cookieInfo")
	}
	return localdb.SetJson(localdb.AcfunUserCookieInfoKey(username), cookieInfo)
}

func GetCookieInfo(username string) (cookieInfo *LoginResponse, err error) {
	err = localdb.GetJson(localdb.AcfunUserCookieInfoKey(username), &cookieInfo)
	return
}

func ClearCookieInfo(username string) error {
	_, err := localdb.Delete(localdb.AcfunUserCookieInfoKey(username), localdb.IgnoreNotFoundOpt())
	return err
}

func (c *StateManager) GetNewsInfo(mid int64) (*NewsInfo, error) {
	var newsInfo = &NewsInfo{}
	err := c.GetJson(c.CurrentNewsKey(mid), newsInfo)
	if err != nil {
		return nil, err
	}
	return newsInfo, nil
}

func (c *StateManager) CheckDynamicId(dynamic int64) (result bool) {
	_, err := c.Get(c.DynamicIdKey(dynamic))
	if err == buntdb.ErrNotFound {
		return true
	}
	return false
}

func (c *StateManager) MarkDynamicId(dynamic int64) (bool, error) {
	var isOverwrite bool
	err := c.Set(c.DynamicIdKey(dynamic), "",
		localdb.SetExpireOpt(time.Hour*120), localdb.SetGetIsOverwriteOpt(&isOverwrite))
	return isOverwrite, err
}

func (c *StateManager) GetNotifyMsg(groupCode int64, notifyKey string) (*message.GroupMessage, error) {
	value, err := c.Get(c.NotifyMsgKey(groupCode, notifyKey))
	if err != nil {
		return nil, err
	}
	return localutils.DeserializationGroupMsg(value)
}

func (c *StateManager) SetGroupCompactMarkIfNotExist(groupCode int64, compactKey string) error {
	return c.Set(c.CompactMarkKey(groupCode, compactKey), "",
		localdb.SetExpireOpt(CompactExpireTime), localdb.SetNoOverWriteOpt())
}

func (c *StateManager) SetNotifyMsg(notifyKey string, msg *message.GroupMessage) error {
	tmp := &message.GroupMessage{
		Id:        msg.Id,
		GroupCode: msg.GroupCode,
		Sender:    msg.Sender,
		Time:      msg.Time,
		Elements: localutils.MessageFilter(msg.Elements, func(e message.IMessageElement) bool {
			return e.Type() == message.Text || e.Type() == message.Image
		}),
	}
	value, err := localutils.SerializationGroupMsg(tmp)
	if err != nil {
		return err
	}
	return c.Set(c.NotifyMsgKey(tmp.GroupCode, notifyKey), value,
		localdb.SetExpireOpt(CompactExpireTime), localdb.SetNoOverWriteOpt())
}
