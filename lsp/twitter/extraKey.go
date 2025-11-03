package twitter

import "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"

// 由于ddbot内置的是一个键值型数据库，通常需要使用一个key获取一个value，所以当需要存储数据的时候，需要使用额外的自定义key
// 可以在这个文件内实现

type ExtraKey struct{}

func (e *ExtraKey) TweetListKey(keys ...interface{}) string {
	return buntdb.TwitterTweetListKey(keys...)
}
func (e *ExtraKey) UserInfoKey(keys ...interface{}) string {
	return buntdb.TwitterUserInfoKey(keys...)
}
func (e *ExtraKey) LatestTweetIdsKey(keys ...interface{}) string {
	return buntdb.LatestTweetIdsKey(keys...)
}
func (e *ExtraKey) LastFreshKey(keys ...interface{}) string {
	return buntdb.TwitterLastFreshKey(keys...)
}
func (e *ExtraKey) CompactMarkKey(keys ...interface{}) string {
	return buntdb.TwitterCompactMarkKey(keys...)
}
func (e *ExtraKey) NotifyMsgKey(keys ...interface{}) string {
	return buntdb.TwitterNotifyMsgKey(keys...)
}
func NewExtraKey() *ExtraKey {
	return &ExtraKey{}
}
