package douyu

import "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"

type keySet struct {
}

func (l *keySet) GroupAtAllMarkKey(keys ...interface{}) string {
	return buntdb.DouyuGroupAtAllMarkKey(keys...)
}

func (l *keySet) GroupConcernConfigKey(keys ...interface{}) string {
	return buntdb.DouyuGroupConcernConfigKey(keys...)
}

func (l *keySet) GroupConcernStateKey(keys ...interface{}) string {
	return buntdb.DouyuGroupConcernStateKey(keys...)
}

func (l *keySet) FreshKey(keys ...interface{}) string {
	return buntdb.DouyuFreshKey(keys...)
}

func (l *keySet) ParseGroupConcernStateKey(key string) (int64, interface{}, error) {
	return buntdb.ParseConcernStateKeyWithInt64(key)
}

type extraKey struct {
}

func (l *extraKey) CurrentLiveKey(keys ...interface{}) string {
	return buntdb.DouyuCurrentLiveKey(keys...)
}

func NewExtraKey() *extraKey {
	return &extraKey{}
}

func NewKeySet() *keySet {
	return &keySet{}
}
