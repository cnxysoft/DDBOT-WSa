package xhs

import (
	"time"

	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

const xhsMarkNoteTTL = time.Hour * 120

type StateManager struct {
	*concern.StateManager
	*extraKey
}

func (c *StateManager) GetGroupConcernConfig(groupCode int64, id interface{}) (concernConfig concern.IConfig) {
	return NewGroupConcernConfig(c.StateManager.GetGroupConcernConfig(groupCode, id))
}

func (c *StateManager) AddUserInfo(userInfo *UserInfo) error {
	if userInfo == nil {
		return nil
	}
	return c.SetJson(c.UserInfoKey(userInfo.Uid), userInfo)
}

func (c *StateManager) GetUserInfo(uid string) (*UserInfo, error) {
	var userInfo = &UserInfo{}
	err := c.GetJson(c.UserInfoKey(uid), userInfo)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (c *StateManager) AddLiveInfo(liveInfo *LiveInfo) error {
	if liveInfo == nil {
		return nil
	}
	return c.SetJson(c.CurrentLiveKey(liveInfo.Uid), liveInfo)
}

func (c *StateManager) AddNewsInfo(newsInfo *NewsInfo) error {
	if newsInfo == nil {
		return nil
	}
	return c.SetJson(c.NewsInfoKey(newsInfo.Uid), newsInfo)
}

func (c *StateManager) GetLiveInfo(uid string) (*LiveInfo, error) {
	var liveInfo = &LiveInfo{}
	err := c.GetJson(c.CurrentLiveKey(uid), liveInfo)
	if err != nil {
		return nil, err
	}
	return liveInfo, nil
}

func (c *StateManager) GetNewsInfo(uid string) (*NewsInfo, error) {
	var newsInfo = &NewsInfo{}
	err := c.GetJson(c.NewsInfoKey(uid), newsInfo)
	if err != nil {
		return nil, err
	}
	return newsInfo, nil
}

func (c *StateManager) RemoveUserInfo(uid string) error {
	_, err := c.Delete(c.UserInfoKey(uid))
	return err
}

func (c *StateManager) RemoveLiveInfo(uid string) error {
	_, err := c.Delete(c.CurrentLiveKey(uid))
	return err
}

func (c *StateManager) RemoveNewsInfo(uid string) error {
	_, err := c.Delete(c.NewsInfoKey(uid))
	return err
}

func (c *StateManager) MarkNoteId(noteID string) (replaced bool, err error) {
	err = c.Set(c.MarkNoteIdKey(noteID), "",
		localdb.SetExpireOpt(xhsMarkNoteTTL),
		localdb.SetGetIsOverwriteOpt(&replaced))
	return
}

func (c *StateManager) Start() error {
	return c.StateManager.Start()
}

func NewStateManager(notify chan<- concern.Notify) *StateManager {
	sm := &StateManager{}
	sm.extraKey = NewExtraKey()
	sm.StateManager = concern.NewStateManagerWithCustomKey(Site, NewKeySet(), notify)
	return sm
}
