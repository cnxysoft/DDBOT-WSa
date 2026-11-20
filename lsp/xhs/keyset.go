package xhs

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
)

type keySet struct {
}

func (k *keySet) GroupConcernStateKey(keys ...interface{}) string {
	return buntdb.XHSGroupConcernStateKey(keys...)
}

func (k *keySet) GroupConcernConfigKey(keys ...interface{}) string {
	return buntdb.XHSGroupConcernConfigKey(keys...)
}

func (k *keySet) FreshKey(keys ...interface{}) string {
	return buntdb.XHSFreshKey(keys...)
}

func (k *keySet) GroupAtAllMarkKey(keys ...interface{}) string {
	return buntdb.XHSGroupAtAllMarkKey(keys...)
}

func (k *keySet) ParseGroupConcernStateKey(key string) (int64, interface{}, error) {
	groupCode, id, err := buntdb.ParseConcernStateKeyWithString(key)
	if err != nil {
		return 0, nil, err
	}
	return groupCode, id, nil
}

type extraKey struct {
}

func (k *extraKey) CurrentLiveKey(keys ...interface{}) string {
	return buntdb.XHSCurrentLiveKey(keys...)
}

func (k *extraKey) NewsInfoKey(keys ...interface{}) string {
	return buntdb.XHSNewsInfoKey(keys...)
}

func (k *extraKey) MarkNoteIdKey(keys ...interface{}) string {
	return buntdb.XHSMarkNoteIdKey(keys...)
}

func (k *extraKey) UserInfoKey(keys ...interface{}) string {
	return buntdb.XHSUserInfoKey(keys...)
}

func NewKeySet() *keySet {
	return &keySet{}
}

func NewExtraKey() *extraKey {
	return &extraKey{}
}
