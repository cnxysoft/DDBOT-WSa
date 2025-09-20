package acfun

import localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"

type ExtraKey struct{}

func (e *ExtraKey) UserInfoKey(keys ...interface{}) string {
	return localdb.AcfunUserInfoKey(keys...)
}

func (e *ExtraKey) LiveInfoKey(keys ...interface{}) string {
	return localdb.AcfunLiveInfoKey(keys...)
}

func (e *ExtraKey) NotLiveKey(keys ...interface{}) string {
	return localdb.AcfunNotLiveKey(keys...)
}

func (e *ExtraKey) UidFirstTimestamp(keys ...interface{}) string {
	return localdb.AcfunUidFirstTimestampKey(keys...)
}

func (k *ExtraKey) CurrentNewsKey(keys ...interface{}) string {
	return localdb.AcfunCurrentNewsKey(keys...)
}

func (k *ExtraKey) DynamicIdKey(keys ...interface{}) string {
	return localdb.AcfunDynamicIdKey(keys...)
}

func (k *ExtraKey) NotifyMsgKey(keys ...interface{}) string {
	return localdb.AcfunNotifyMsgKey(keys...)
}

func (k *ExtraKey) CompactMarkKey(keys ...interface{}) string {
	return localdb.AcfunCompactMarkKey(keys...)
}

func NewExtraKey() *ExtraKey {
	return &ExtraKey{}
}
