package douyin

import "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"

// 由于ddbot内置的是一个键值型数据库，通常需要使用一个key获取一个value，所以当需要存储数据的时候，需要使用额外的自定义key
// 可以在这个文件内实现

type ExtraKey struct{}

func (e *ExtraKey) UserInfoKey(keys ...interface{}) string {
	return buntdb.DouyinUserInfoKey(keys...)
}
func (e *ExtraKey) CurrentLiveKey(keys ...interface{}) string {
	return buntdb.DouyinCurrentLiveKey(keys...)
}

func (e *ExtraKey) DynamicIdKey(keys ...interface{}) string {
	return buntdb.DouyinDynamicIdKey(keys...)
}
func (e *ExtraKey) FreshKey(keys ...interface{}) string {
	return buntdb.DouyinFreshKey(keys...)
}

func (k *ExtraKey) NotifyMsgKey(keys ...interface{}) string {
	return buntdb.DouyinNotifyMsgKey(keys...)
}
func (k *ExtraKey) CompactMarkKey(keys ...interface{}) string {
	return buntdb.DouyinCompactMarkKey(keys...)
}

func (k *ExtraKey) UidFirstTimestamp(keys ...interface{}) string {
	return buntdb.DouyinUidFirstTimestampKey(keys...)
}

// NotLiveConfirmKey 下播连续确认计数的键。
// 成品 ExtraKey.NotLiveConfirmKey（extraKey.go:16，416B）未走 buntdb 的具名封装，
// 而是直接用通用 buntdb.Key 拼 "DouyinNotLiveConfirm:<uid>"（rodata 0x10b51d3）。
func (k *ExtraKey) NotLiveConfirmKey(keys ...interface{}) string {
	all := make([]interface{}, 0, len(keys)+1)
	all = append(all, "DouyinNotLiveConfirm")
	all = append(all, keys...)
	return buntdb.Key(all...)
}

func NewExtraKey() *ExtraKey {
	return &ExtraKey{}
}
