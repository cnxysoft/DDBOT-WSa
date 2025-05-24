// 别忘记改package name
package twitter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/mmcdole/gofeed"
)

const (
	// 这个名字是日志中的名字，如果不知道取什么名字，可以和Site一样
	ConcernName = "twitter-concern"

	// 插件支持的网站名
	Site = "twitter"
	// 这个插件支持的订阅类型可以像这样自定义，然后在 Types 中返回
	Tweets concern_type.Type = "news"
	// 当像这样定义的时候，支持 /watch -s mysite -t type1 id
	// 当实现的时候，请修改上面的定义
	// API Base URL
	XUrl       = "https://x.com"
	XImgHost   = "https://pbs.twimg.com"
	XVideoHost = "https://video.twimg.com"
	//BaseURL = "https://lightbrd.com/"
	//alt1BaseURL = "https://nitter.privacydev.net/%s/rss"
	//TweetAPI = "https://cdn.syndication.twimg.com/tweet-result?id=%s&token=%s"
)

var (
	logger          = utils.GetModuleLogger(ConcernName)
	requestInterval = time.Second * 5 // 每个请求之间的间隔
	buildProfileURL = func(screenName string) string {
		return BaseURL[rand.Intn(len(BaseURL))] + screenName
	}
)

type twitterStateManager struct {
	*concern.StateManager
}

// GetGroupConcernConfig 重写 concern.StateManager 的GetGroupConcernConfig方法，让我们自己定义的 GroupConcernConfig 生效
func (t *twitterStateManager) GetGroupConcernConfig(groupCode int64, id interface{}) concern.IConfig {
	return NewGroupConcernConfig(t.StateManager.GetGroupConcernConfig(groupCode, id))
}

type twitterConcern struct {
	*twitterStateManager
	*extraKey
	parser       *gofeed.Parser
	newUsersChan chan interface{} // 新用户通知通道
}

func (t *twitterConcern) Site() string {
	return Site
}

func (t *twitterConcern) Types() []concern_type.Type {
	return []concern_type.Type{Tweets}
}

func (t *twitterConcern) ParseId(s string) (interface{}, error) {
	// 在这里解析id
	// 此处返回的id类型，即是其他地方id interface{}的类型
	// 其他所有地方的id都由此函数生成
	// 推荐在string 或者 int64类型中选择其一
	// 如果订阅源有uid等数字唯一标识，请选择int64，如 bilibili
	// 如果订阅源有数字并且有字符，请选择string， 如 douyu
	if strings.HasPrefix(s, "@") {
		return strings.TrimPrefix(s, "@"), nil
	}
	return s, nil
}

//func buildProfileURL(screenName string) string {
//	//return strings.ReplaceAll(BaseURL, "%s", screenName)
//	return BaseURL[rand.Intn(len(BaseURL))] + screenName
//}

func CSTTime(t time.Time) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return t.In(loc)
}

func (t *twitterConcern) FindUserInfo(id string, refresh bool) (*UserInfo, error) {
	var info *UserInfo
	if refresh {
		Url := buildProfileURL(id)
		opts := GetRequestOptions()
		var resp bytes.Buffer
		var respHeaders requests.RespHeader
		if err := requests.GetWithHeader(Url, nil, &resp, &respHeaders, opts...); err != nil {
			logger.Errorf("请求用户信息失败：%v", err)
			return nil, err
		}

		// 解压缩HTML
		body, err := HtmlDecoder(respHeaders, resp)
		if err != nil {
			logger.WithField("Url", Url).WithField("User", id).Errorf("解压缩HTML失败：%v", err)
			return nil, err
		}

		// 解析用户信息
		profile, _, err := ParseResp(body, Url)
		if err != nil {
			return nil, err
		} else if profile == nil {
			return nil, errors.New("用户不存在或返回结果为空")
		}
		info = &UserInfo{
			Id:              profile.ScreenName,
			Name:            profile.Name,
			ProfileImageUrl: profile.AvatarURL,
		}
		//data := io.Reader(&resp)
		//feed, err := t.parser.Parse(data)
		//if err != nil {
		//	return nil, fmt.Errorf("parse UserInfo error: %v", err)
		//}
		//info = &UserInfo{
		//	Id:              id,
		//	Name:            feed.Title,
		//	ProfileImageUrl: feed.Image.URL,
		//}
		//info.Name = GetShortName(feed)
		err = t.AddUserInfo(info)
		if err != nil {
			return nil, err
		}
	}
	return t.GetUserInfo(id)
}

func (t *twitterConcern) FindOrLoadUserInfo(id string) (*UserInfo, error) {
	info, _ := t.FindUserInfo(id, false)
	if info == nil {
		return t.FindUserInfo(id, true)
	}
	return info, nil
}

func (t *twitterConcern) GetUserInfo(id string) (*UserInfo, error) {
	var userInfo *UserInfo
	err := t.GetJson(t.UserInfoKey(id), &userInfo)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (t *twitterConcern) AddUserInfo(info *UserInfo) error {
	if info == nil {
		return errors.New("<nil userInfo>")
	}
	return t.SetJson(t.UserInfoKey(info.Id), info)
}

func (t *twitterConcern) AddNewsInfo(info *NewsInfo) error {
	if info == nil {
		return errors.New("<nil NewsInfo>")
	}
	return t.RWCover(func() error {
		var err error
		err = t.SetJson(t.UserInfoKey(info.UserInfo.Id), info.UserInfo)
		if err != nil {
			return err
		}
		return t.SetJson(t.NewsInfoKey(info.UserInfo.Id), info)
	})
}

func (t *twitterConcern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	// 这里是添加订阅的函数
	// 可以使 c.StateManager.AddGroupConcern(groupCode, id, ctype) 来添加这个订阅
	// 通常在添加订阅前还需要通过id访问网站上的个人信息页面，来确定id是否存在，是否可以正常订阅
	info, err := t.FindOrLoadUserInfo(id.(string))
	if err != nil {
		return nil, fmt.Errorf("查询用户信息失败 %v - %v", id, err)
	}
	err = t.AddNewsInfo(&NewsInfo{
		UserInfo:     info,
		LatestNewsTs: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("添加订阅失败 - 内部错误 - %v", err)
	}
	_, err = t.GetStateManager().AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	select {
	case t.newUsersChan <- id:
	default:
		logger.Warnf("新用户通知队列已满，将在下个周期刷新，userId: %v", id)
	}
	return info, nil
}

func (t *twitterConcern) removeNewsInfo(id string) error {
	_, err := t.Delete(t.NewsInfoKey(id), buntdb.IgnoreNotFoundOpt())
	return err
}

func (t *twitterConcern) removeUserInfo(id string) error {
	_, err := t.Delete(t.UserInfoKey(id), buntdb.IgnoreNotFoundOpt())
	return err
}

func (t *twitterConcern) Remove(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	// 大部分时候简单的删除即可
	// 如果还有更复杂的逻辑可以自由实现
	identity, _ := t.Get(id)
	_, err := t.GetStateManager().RemoveGroupConcern(groupCode, id.(string), ctype)
	if err != nil {
		return nil, err
	}

	if err = t.removeNewsInfo(id.(string)); err != nil {
		if err != errors.New("not found") {
			logger.WithError(err).Errorf("removeNewsInfo error")
		} else {
			err = nil
		}
	}

	if err = t.removeUserInfo(id.(string)); err != nil {
		if err != errors.New("not found") {
			logger.WithError(err).Errorf("removeUserInfo error")
		} else {
			err = nil
		}
	}

	if identity == nil {
		identity = concern.NewIdentity(id, "unknown")
	}
	return identity, err
}

func (t *twitterConcern) Get(id interface{}) (concern.IdentityInfo, error) {
	// 查看一个订阅的信息
	// 通常是查看数据库中是否有id的信息，如果没有可以去网页上获取
	usrInfo, err := t.GetUserInfo(id.(string))
	if err != nil {
		return nil, errors.New("GetUserInfo error")
	}
	return concern.NewIdentity(usrInfo.Id, usrInfo.Name), nil
}

func (t *twitterConcern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, event concern.Event) []concern.Notify {
		switch event.(type) {
		case *NewsInfo:
			return []concern.Notify{&NewNotify{groupCode, event.(*NewsInfo)}}
		default:
			logger.Errorf("unknown EventType %+v", event)
			return nil
		}
	}
}

// 新增辅助函数获取刷新间隔
func getRefreshInterval() time.Duration {
	if config.GlobalConfig != nil {
		interval := config.GlobalConfig.GetDuration("twitter.interval")
		if interval > 0 {
			return interval
		}
	}
	return time.Second * 30
}

func (t *twitterConcern) processUser(ctx context.Context, eventChan chan<- concern.Event, userId interface{}) {
	if ctx.Err() != nil {
		return
	}
	events, err := t.freshNewsInfo(Tweets, userId)
	if err != nil {
		//logger.WithError(err).WithField("userId", userId).Error("刷新用户推文失败")
		return
	}
	for _, e := range events {
		eventChan <- e
	}
}

func (t *twitterConcern) processUsers(ctx context.Context, eventChan chan<- concern.Event) {
	// 获取最新用户列表
	_, ids, _, _ := t.StateManager.ListConcernState(func(g int64, id interface{}, p concern_type.Type) bool { return p.ContainAll(Tweets) })
	for _, userId := range ids {
		if ctx.Err() != nil {
			return
		}
		// 执行处理逻辑（与之前相同）
		events, err := t.freshNewsInfo(Tweets, userId)
		if err != nil {
			//logger.WithError(err).WithField("userId", userId).Error("刷新用户推文失败")
			continue
		}
		for _, e := range events {
			eventChan <- e
		}
		// 添加随机间隔（避免请求对齐）
		time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
	}
}

func (t *twitterConcern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		interval := getRefreshInterval()
		ti := time.NewTimer(time.Second * 3)
		defer ti.Stop() // 确保定时器资源释放

		for {
			select {
			case userId := <-t.newUsersChan:
				go func(uid interface{}) { // 使用goroutine替代AfterFunc
					select {
					case <-time.After(time.Duration(rand.Int63n(10)) * time.Second):
						t.processUser(ctx, eventChan, uid)
					case <-ctx.Done():
						return
					}
				}(userId)
			case <-ti.C:
				t.processUsers(ctx, eventChan)
				ti.Reset(interval) // 重置定时器
			case <-ctx.Done():
				return
			}
		}
	}
}

func (t *twitterConcern) freshNewsInfo(ctype concern_type.Type, id interface{}) ([]concern.Event, error) {
	var result []concern.Event
	userId := id.(string)
	if ctype.ContainAll(Tweets) {
		userInfo, err := t.FindOrLoadUserInfo(userId)
		newsInfo := &NewsInfo{UserInfo: userInfo}
		if err != nil {
			logger.Errorf("查找用户信息失败：%v", err)
		}
		newTweets, err := t.GetTweets(userId)
		if err != nil {
			return nil, err
		}
		oldNewsInfo, err := t.GetNewsInfo(userId)
		if err != nil {
			return nil, err
		}
		newsInfo.LatestNewsTs = time.Now().UTC()
		if len(newTweets) > 0 && newTweets[0].ID != "" {
			//newsInfo.LatestNewsTs = t.GetLatestNewsTs(newTweets)
			newsInfo.LatestTweetId = newTweets[0].ID
			if oldNewsInfo == nil || (newsInfo.LatestTweetId != oldNewsInfo.LatestTweetId) {
				if oldNewsInfo == nil || oldNewsInfo.LatestTweetId == "" {
					oldNewsInfo = &NewsInfo{
						UserInfo:      userInfo,
						LatestNewsTs:  newsInfo.LatestNewsTs,
						LatestTweetId: newsInfo.LatestTweetId,
					}
				}
				// 获取超过最后推送时间的tweet
				//NewTweets := t.GetNewTweetsFromTime(oldNewsInfo.LatestNewsTs, newTweets)
				NewTweets := t.GetNewTweetsFromTweetId(oldNewsInfo, newTweets)
				if len(NewTweets) > 0 {
					t.reverseTweets(NewTweets)
					// 将新的tweet添加到result中
					for _, tweet := range NewTweets {
						res := &NewsInfo{
							UserInfo:     userInfo,
							Tweet:        tweet,
							LatestNewsTs: tweet.CreatedAt,
						}
						if tweet.RtType() == RETWEET {
							res.LatestNewsTs = newsInfo.LatestNewsTs
						}
						result = append(result, res)
					}
				} else {
					newsInfo.LatestTweetId = oldNewsInfo.LatestTweetId
				}
			}
		} else {
			if oldNewsInfo != nil && oldNewsInfo.LatestTweetId != "" {
				newsInfo.LatestTweetId = oldNewsInfo.LatestTweetId
			}
		}
		if newsInfo.LatestTweetId != "" {
			err = t.AddNewsInfo(newsInfo)
			if err != nil {
				logger.Errorf("内部错误 - 推送信息更新失败：%v", err)
				return nil, err
			}
		}
	}
	return result, nil
}

func (t *twitterConcern) SetLastFreshTime(ts int64) error {
	return t.SetInt64(t.LastFreshKey(), ts)
}
func (t *twitterConcern) GetLastFreshTime() (int64, error) {
	return t.GetInt64(t.LastFreshKey())
}

func GetRequestOptions() []requests.Option {
	h1 := (http.DefaultTransport).(*http.Transport).Clone()
	h1.MaxResponseHeaderBytes = 262144
	return []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 10),
		requests.AddUAOption(UserAgent),
		requests.RequestAutoHostOption(),
		requests.CookieOption("cf_clearance", cfClearance),
		requests.CookieOption("hlsPlayback", "on"),
		requests.HeaderOption("Connection", "keep-alive"),
		requests.HeaderOption("Accept",
			"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br, zstd"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"),
		requests.HeaderOption("sec-ch-ua-platform-version", "19.0.0"),
		requests.HeaderOption("sec-ch-ua-model", "navigate"),
		requests.HeaderOption("Sec-Fetch-Site", "none"),
		requests.HeaderOption("sec-ch-ua", "\"Microsoft Edge\";v=\"135\", \"Not-A.Brand\";v=\"8\", \"Chromium\";v=\"135\""),
		requests.HeaderOption("sec-ch-ua-mobile", "?0"),
		requests.HeaderOption("sec-ch-ua-platform", "\"Windows\""),
		requests.HeaderOption("sec-ch-ua-full-version", "\"135.0.3179.73\""),
		requests.HeaderOption("sec-ch-ua-arch", "\"x86\""),
		requests.HeaderOption("sec-ch-ua-bitness", "64"),
		requests.HeaderOption("sec-ch-ua-full-version-list",
			"\"Microsoft Edge\";v=\"135.0.3179.73\", \"Not-A.Brand\";v=\"8.0.0.0\", \"Chromium\";v=\"135.0.7049.85\""),
		requests.HeaderOption("Upgrade-Insecure-Requests", "1"),
		requests.HeaderOption("Sec-Fetch-Dest", "document"),
		requests.HeaderOption("Sec-Fetch-Mode", "navigate"),
		requests.HeaderOption("Sec-Fetch-User", "?1"),
		requests.HeaderOption("priority", "u=0, i"),
		requests.RetryOption(3),
		requests.WithTransport(h1),
	}
}

func HtmlDecoder(respHeaders requests.RespHeader, resp bytes.Buffer) ([]byte, error) {
	var body []byte
	if encoding := respHeaders.ContentEncoding; encoding != "" {
		body = resp.Bytes()
		switch encoding {
		case "gzip":
			body, _ = decompressGzip(body)
		case "deflate":
			body, _ = decompressDeflate(body)
		case "br":
			body, _ = decompressBrotli(body)
		case "zstd":
			body, _ = decompressZstd(body)
		default:
			logger.Warnf("不支持的压缩格式: %s", encoding)
		}
	} else {
		body = resp.Bytes()
	}
	return body, nil
}

func (t *twitterConcern) GetTweets(id string) ([]Tweet, error) {
	Url := buildProfileURL(id)
	opts := GetRequestOptions()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, nil, &resp, &respHeaders, opts...); err != nil {
		logger.WithField("Url", Url).WithField("userId", id).Errorf("获取推文列表失败：%v", err)
		return nil, err
	}
	//data := io.Reader(&resp)
	//feed, err := t.parser.Parse(data)
	//if err != nil {
	//	return nil, fmt.Errorf("GetTweets error: %v", err)
	//}
	// 解压缩HTML
	body, err := HtmlDecoder(respHeaders, resp)
	if err != nil {
		logger.WithField("Url", Url).WithField("userId", id).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}

	// 解析解压后的数据
	_, tweets, err := ParseResp(body, Url)
	if err != nil {
		logger.WithField("Url", Url).WithField("userId", id).Errorf("解析HTML失败：%v", err)
		return nil, err
	} else if tweets == nil {
		logger.Warn("获取推文列表失败：无法解析数据或推文列表为空")
		return nil, nil
	}
	//var result []*TweetItem
	//for _, item := range tweets {
	//	result = append(result, &TweetItem{
	//		Type:      item.RtType(),
	//		Title:     item.Content,
	//		Link:      item.Url,
	//		Media:     item.Media,
	//		Published: item.CreatedAt,
	//		Author: &UserInfo{
	//			Id:   item.OrgUser.ScreenName,
	//			Name: item.OrgUser.Name,
	//		},
	//	})
	//}
	// 时间排序
	//t.sortTweetsByPublished(result)
	return tweets, nil
}

//func (t *twitterConcern) sortTweetsByPublished(tweets []*TweetItem) {
//	sort.SliceStable(tweets, func(i, j int) bool {
//		return tweets[i].Published.Before(tweets[j].Published)
//	})
//}

func (t *twitterConcern) reverseTweets(s []Tweet) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func (t *twitterConcern) GetNewTweetsFromTweetId(oldNewsInfo *NewsInfo, tweets []Tweet) []Tweet {
	if index := findTweetIndex(tweets, oldNewsInfo.LatestTweetId); index >= 0 {
		return tweets[:index]
	}
	var retTweets []Tweet
	for _, tweet := range tweets {
		oldTime, err := ParseSnowflakeTimestamp(oldNewsInfo.LatestTweetId)
		if err != nil {
			logger.WithError(err).Errorf("ParseSnowflakeTimestamp error")
			continue
		}
		if tweets[0].CreatedAt.Before(oldTime) {
			continue
		}
		if tweets[0].CreatedAt.After(oldNewsInfo.LatestNewsTs) {
			retTweets = append(retTweets, tweet)
		}
	}
	return retTweets
}

func findTweetIndex(tweets []Tweet, targetID string) int {
	for i, tweet := range tweets {
		if tweet.ID == targetID {
			return i
		}
	}
	return -1
}

//func (t *twitterConcern) GetNewTweetsFromTime(oldTime time.Time, item []*TweetItem) []*TweetItem {
//	var result []*TweetItem
//	for _, tweet := range item {
//		if tweet.Published.After(oldTime) {
//			result = append(result, tweet)
//		}
//	}
//	return result
//}

func (t *twitterConcern) GetLatestNewsTs(tweets []*TweetItem) time.Time {
	return tweets[len(tweets)-1].Published
}

func (t *twitterConcern) GetNewsInfo(id string) (*NewsInfo, error) {
	var newsInfo *NewsInfo
	err := t.GetJson(t.NewsInfoKey(id), &newsInfo)
	if err != nil {
		return nil, err
	}
	return newsInfo, nil
}

func (t *twitterConcern) Start() error {
	// 以用户设置覆盖默认设置
	setCookies()
	// 如果需要启用轮询器，可以使用下面的方法
	t.UseEmitQueue()
	// 下面两个函数是订阅的关键，需要实现，请阅读文档
	t.StateManager.UseFreshFunc(t.fresh())
	t.StateManager.UseNotifyGeneratorFunc(t.notifyGenerator())
	return t.StateManager.Start()
}

func (t *twitterConcern) Stop() {
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)
	t.StateManager.Stop()
	logger.Tracef("%v StateManager已停止", Site)
	logger.Tracef("%v concern已停止", Site)
}

func (t *twitterConcern) GetStateManager() concern.IStateManager {
	return t.StateManager
}

func newConcern(notifyChan chan<- concern.Notify) *twitterConcern {
	// 默认是string格式的id
	sm := &twitterStateManager{concern.NewStateManagerWithStringID(Site, notifyChan)}
	// 如果要使用int64格式的id，可以用下面的
	//c.StateManager = concern.NewStateManagerWithInt64ID(Site, notifyChan)
	return &twitterConcern{sm, new(extraKey), gofeed.NewParser(), make(chan interface{}, 10)}
}
