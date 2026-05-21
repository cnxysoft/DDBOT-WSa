package youtube

import (
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/tidwall/buntdb"
	"time"
)

type StateManager struct {
	*concern.StateManager
	*extraKey
}

func (s *StateManager) AddInfo(info *Info) error {
	return s.SetJson(s.InfoKey(info.ChannelId), info, localdb.SetExpireOpt(time.Hour*24*7))
}

func (s *StateManager) GetInfo(channelId string) (*Info, error) {
	info := new(Info)
	err := s.GetJson(s.InfoKey(channelId), info)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (s *StateManager) GetVideo(channelId string, videoId string) (*VideoInfo, error) {
	var v *VideoInfo
	err := s.GetJson(s.VideoKey(channelId, videoId), &v)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (s *StateManager) AddVideo(v *VideoInfo) error {
	return s.SetJson(s.VideoKey(v.ChannelId, v.VideoId), v)
}

func (s *StateManager) DeleteLiveState(channelId string) error {
	info, _ := s.GetInfo(channelId)

	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		var keep []*VideoInfo
		if info != nil {
			for _, v := range info.VideoInfo {
				if v == nil {
					continue
				}
				if !v.IsLive() {
					keep = append(keep, v)
				}
			}
		}

		var deleteKeys []string
		err := tx.AscendKeys(s.VideoKey(channelId, "*"), func(key, value string) bool {
			var v *VideoInfo
			if unmarshalErr := json.Unmarshal([]byte(value), &v); unmarshalErr == nil && v != nil && v.IsLive() {
				deleteKeys = append(deleteKeys, key)
			}
			return true
		})
		if err != nil {
			return err
		}

		for _, key := range deleteKeys {
			if _, err := tx.Delete(key); err != nil && err != buntdb.ErrNotFound {
				return err
			}
		}

		if info == nil {
			return nil
		}
		if len(keep) == 0 {
			if _, err := tx.Delete(s.InfoKey(channelId)); err != nil && err != buntdb.ErrNotFound {
				return err
			}
			return nil
		}

		updated := *info
		updated.VideoInfo = keep
		data, err := json.Marshal(&updated)
		if err != nil {
			return err
		}
		_, _, err = tx.Set(s.InfoKey(channelId), string(data), &buntdb.SetOptions{
			Expires: true,
			TTL:     time.Hour * 24 * 7,
		})
		return err
	})
}

func (s *StateManager) GetGroupConcernConfig(groupCode int64, id interface{}) (concernConfig concern.IConfig) {
	return NewGroupConcernConfig(s.StateManager.GetGroupConcernConfig(groupCode, id))
}

func NewStateManager(notify chan<- concern.Notify) *StateManager {
	sm := new(StateManager)
	sm.extraKey = NewExtraKey()
	sm.StateManager = concern.NewStateManagerWithCustomKey(Site, NewKeySet(), notify)
	return sm
}
