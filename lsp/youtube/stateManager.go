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
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		// Read current info within this transaction
		info := new(Info)
		val, err := tx.Get(s.InfoKey(channelId))
		if err != nil {
			if err == buntdb.ErrNotFound {
				return nil // Nothing to delete
			}
			return err
		}
		if err := json.Unmarshal([]byte(val), info); err != nil {
			return err
		}

		// Check if there's anything to clean up
		if info == nil || len(info.VideoInfo) == 0 {
			return nil
		}

		// Separate live vs non-live entries
		var keep []*VideoInfo
		for _, v := range info.VideoInfo {
			if v != nil && !v.IsLive() {
				keep = append(keep, v)
			}
		}

		// Delete all live video entries
		deleteKeys := make([]string, 0)
		tx.AscendKeys(s.VideoKey(channelId, "*"), func(key, value string) bool {
			var v *VideoInfo
			if unmarshalErr := json.Unmarshal([]byte(value), &v); unmarshalErr == nil && v != nil && v.IsLive() {
				deleteKeys = append(deleteKeys, key)
			}
			return true
		})

		for _, key := range deleteKeys {
			if _, err := tx.Delete(key); err != nil && err != buntdb.ErrNotFound {
				return err
			}
		}

		// Update or delete info
		if len(keep) == 0 {
			_, _ = tx.Delete(s.InfoKey(channelId))
		} else {
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
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// getInfoWithoutCache reads info directly from DB without caching layer
func (s *StateManager) getInfoWithoutCache(channelId string) (*Info, error) {
	db, err := localdb.GetClient()
	if err != nil {
		return nil, err
	}
	var info *Info
	err = db.View(func(tx *buntdb.Tx) error {
		val, err := tx.Get(s.InfoKey(channelId))
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(val), &info)
	})
	if err != nil {
		return nil, err
	}
	return info, nil
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
