package xhh

import (
	"errors"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"time"
)

type StateManager struct {
	*concern.StateManager
	extraKeySet
}

func NewStateManager(notify chan<- concern.Notify) *StateManager {
	return &StateManager{
		StateManager: concern.NewStateManagerWithStringID(Site, notify),
	}
}

type extraKeySet struct{}

func (*extraKeySet) UserInfoKey(keys ...interface{}) string {
	return localdb.XHHUserInfoKey(keys...)
}

func (*extraKeySet) NewsInfoKey(keys ...interface{}) string {
	return localdb.XHHNewsInfoKey(keys...)
}

func (*extraKeySet) MarkMomentIdKey(keys ...interface{}) string {
	return localdb.XHHMarkMomentIdKey(keys...)
}

// AddUserInfoWithKey 使用指定的key存储用户信息
func (s *StateManager) AddUserInfoWithKey(key string, info *UserInfo) error {
	if info == nil {
		return errors.New("<nil userInfo>")
	}
	return s.SetJson(s.UserInfoKey(key), info)
}

func (s *StateManager) GetUserInfo(userid string) (*UserInfo, error) {
	var userInfo *UserInfo
	err := s.GetJson(s.UserInfoKey(userid), &userInfo)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (s *StateManager) AddNewsInfo(info *NewsInfo) error {
	if info == nil {
		return errors.New("<nil newsInfo>")
	}
	return s.RWCover(func() error {
		var err error
		err = s.SetJson(s.UserInfoKey(info.Userid), info.UserInfo)
		if err != nil {
			return err
		}
		return s.SetJson(s.NewsInfoKey(info.Userid), info)
	})
}

func (s *StateManager) GetNewsInfo(userid string) (*NewsInfo, error) {
	var newsInfo *NewsInfo
	err := s.GetJson(s.NewsInfoKey(userid), &newsInfo)
	if err != nil {
		return nil, err
	}
	return newsInfo, nil
}

// MarkMomentId 标记已推送的动态ID
func (s *StateManager) MarkMomentId(momentId string) (replaced bool, err error) {
	err = s.Set(s.MarkMomentIdKey(momentId), "",
		localdb.SetExpireOpt(time.Hour*120), localdb.SetGetIsOverwriteOpt(&replaced))
	return
}

func (s *StateManager) RemoveUserInfo(userid string) error {
	_, err := s.Delete(s.UserInfoKey(userid))
	if err != nil {
		return err
	}
	return nil
}

func (s *StateManager) RemoveNewsInfo(userid string) error {
	_, err := s.Delete(s.NewsInfoKey(userid))
	if err != nil {
		return err
	}
	return nil
}
