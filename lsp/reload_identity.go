package lsp

import (
	"sync"

	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
)

// PrepareBotIdentity reserves the persistent identity before a first binding can
// emit any events. A later preparation/persistence failure restores the previous
// record; established identities remain immutable in this data directory.
func PrepareBotIdentity(identity string) (func(), func(), error) {
	return prepareBotIdentity(buntdbIdentityStore{db: localdb.MustGetClient()}, identity)
}

func prepareBotIdentity(store botIdentityStore, identity string) (func(), func(), error) {
	recorded, err := store.load()
	if err != nil {
		return nil, nil, err
	}
	write, err := decideBotIdentity(recorded, identity)
	if err != nil || !write {
		return nil, nil, err
	}
	if err = store.save(identity); err != nil {
		return nil, nil, err
	}
	var once sync.Once
	return func() { once.Do(func() {}) }, func() {
		once.Do(func() {
			if err := store.save(recorded); err != nil {
				logger.Error("首次绑定失败后无法还原机器人身份记录，请检查数据库磁盘状态")
			}
		})
	}, nil
}
