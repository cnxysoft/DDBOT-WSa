package twitter

import "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"

// 由于ddbot内置的是一个键值型数据库，通常需要使用一个key获取一个value，所以当需要存储数据的时候，需要使用额外的自定义key
// 可以在这个文件内实现

type extraKey struct{}

func (e *extraKey) FreshKey(keys ...interface{}) string {
	return buntdb.TwitterFreshKey(keys...)
}
func (e *extraKey) UserInfoKey(keys ...interface{}) string {
	return buntdb.TwitterUserInfoKey(keys...)
}
func (e *extraKey) NewsInfoKey(keys ...interface{}) string {
	return buntdb.TwitterNewsInfoKey(keys...)
}
func (e *extraKey) LastFreshKey(keys ...interface{}) string {
	return buntdb.TwitterLastFreshKey(keys...)
}
