package twitter

import (
	"errors"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

const (
	ModeAPI                  = "api"
	ModeMirror               = "mirror"
	APIFetchModeHomeTimeline = "home_timeline"
	APIFetchModePerUser      = "per_user"
)

var (
	BaseURL     = []string{"https://nitter.tiekoetter.com/", "https://nitter.catsarch.com/"}
	UserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	twitterAPI  *TwitterAPI
	TwitterMode = ModeMirror
	// TwitterAPIFetchMode controls how API mode obtains subscribed tweets.
	TwitterAPIFetchMode = APIFetchModeHomeTimeline
)

func init() {
	concern.RegisterConcern(newConcern(concern.GetNotifyChan()))
}

func normalizeAPIFetchMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", APIFetchModeHomeTimeline, "home", "home-timeline":
		return APIFetchModeHomeTimeline
	case APIFetchModePerUser, "user", "user_tweets", "user-tweets":
		return APIFetchModePerUser
	default:
		logger.Warnf("未知的 twitter.apiFetchMode=%q，回退到 %s", value, APIFetchModeHomeTimeline)
		return APIFetchModeHomeTimeline
	}
}

func apiFetchModeNeedsFollow() bool {
	return TwitterAPIFetchMode != APIFetchModePerUser
}

// cleanupTmpDir 清理 ./tmp 目录下的所有临时文件
func cleanupTmpDir() {
	tmpDir := filepath.Clean("./tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warnf("清理 tmp 目录失败: %v", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filePath := filepath.Join(tmpDir, entry.Name())
		// 防止路径遍历攻击
		if filepath.Dir(filePath) != tmpDir {
			logger.Warnf("跳过非法路径: %s", filePath)
			continue
		}
		if err := os.Remove(filePath); err != nil {
			logger.Warnf("清理临时文件 %s 失败: %v", filePath, err)
		} else {
			logger.Debugf("已清理临时文件: %s", filePath)
		}
	}
}

func setCookies() {
	ua := config.GlobalConfig.GetString("twitter.userAgent")
	url := config.GlobalConfig.GetStringSlice("twitter.BaseUrl")
	Cookie, _ = cookiejar.New(nil)
	if ua != "" {
		UserAgent = ua
	}
	if len(url) > 0 {
		BaseURL = url
	}
	TwitterAPIFetchMode = normalizeAPIFetchMode(config.GlobalConfig.GetString("twitter.apiFetchMode"))

	mode := strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString("twitter.mode")))
	TwitterMode = resolveTwitterMode(mode)

	if TwitterMode == ModeAPI {
		ct0 := config.GlobalConfig.GetString("twitter.ct0")
		authToken := config.GlobalConfig.GetString("twitter.auth_token")
		bearerToken := config.GlobalConfig.GetString("twitter.bearerToken")
		queryId := config.GlobalConfig.GetString("twitter.queryId")
		userTweetsQueryId := config.GlobalConfig.GetString("twitter.userTweetsQueryId")
		screenName := config.GlobalConfig.GetString("twitter.screenName")

		twitterAPI = NewTwitterAPI(ct0, authToken, bearerToken, queryId, screenName)
		if twitterAPI != nil {
			twitterAPI.SetUserTimelineQueryId(userTweetsQueryId)
		}

		// 自动获取 screenName 和 queryId
		if twitterAPI != nil && twitterAPI.IsEnabled() {
			if screenName == "" {
				logger.Info("Cookie验证：正在获取账号信息...")
				if sn, _, err := verifyTwitterAPI(); err == nil && sn != "" {
					logger.Infof("Cookie验证成功！账号: %s", sn)
				} else {
					reason := "未知原因"
					if err != nil {
						reason = err.Error()
					}
					logger.Errorf("Twitter Cookie验证失败：%v，进入自动恢复模式", err)
					enterTwitterRecovering(reason)
				}
			} else {
				logger.Infof("使用配置的screenName: %s", screenName)
				twitterAPI.SetScreenName(screenName)

				// 从 sw.js 刷新 queryId 缓存
				logger.Info("正在从 sw.js 刷新 queryId 缓存...")
				if err := RefreshAPIFromMainJS(); err != nil {
					logger.Warnf("获取 queryId 失败，使用默认配置: %v", err)
				} else {
					logger.Infof("成功获取 queryId: %s", twitterAPI.GetQueryId())
				}
			}
		}
	}
}

func IsTwitterEnabled() bool {
	return TwitterMode == ModeAPI && twitterAPI != nil && twitterAPI.IsEnabled() && !twitterRecovering.Load()
}

func IsMirrorMode() bool {
	return TwitterMode == ModeMirror
}

// resolveTwitterMode 解析twitter.mode配置。未配置或缺省时保持mirror，
// 与历史版本兼容：存量用户配置里没有这个键时不应被静默切到API模式，
// 要用API需显式配置 twitter.mode: api。
func resolveTwitterMode(mode string) string {
	if mode == ModeAPI {
		return ModeAPI
	}
	return ModeMirror
}

// verifyTwitterAPI 单次验证Cookie：拉取账号信息并刷新queryId缓存。
// 验证成功会就地设置twitterAPI的screenName和mainJSURL。
func verifyTwitterAPI() (screenName, mainJsUrl string, err error) {
	if twitterAPI == nil || !twitterAPI.IsEnabled() {
		return "", "", errors.New("Twitter API 未配置 Cookie")
	}
	sn, mjUrl, err := twitterAPI.FetchInitialState()
	if err != nil {
		return "", "", err
	}
	if sn == "" {
		return "", "", errors.New("screenName为空")
	}
	twitterAPI.SetScreenName(sn)
	twitterAPI.mainJSURL = mjUrl
	// 获取 queryId（从 sw.js → LoggedInMain 提取缓存），失败不阻塞验证
	if err := RefreshAPIFromMainJS(mjUrl); err != nil {
		logger.Warnf("获取 queryId 失败，使用默认配置: %v", err)
	} else {
		logger.Infof("成功获取 queryId: %s", twitterAPI.GetQueryId())
	}
	return sn, mjUrl, nil
}

var (
	twitterRecovering      atomic.Bool
	twitterRecoveringSince atomic.Int64
	twitterVerifyFunc      = func() (string, string, error) { return verifyTwitterAPI() }
	// twitterRecoveryBackoff 自动恢复重试的退避序列，超出后按最后一项封顶
	twitterRecoveryBackoff = []time.Duration{
		15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
		5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	}
)

// enterTwitterRecovering 进入自动恢复模式：暂停Twitter刷新、向管理员发一次告警，
// 并后台按退避节奏重试验证，成功后自动恢复推送并通知。重复调用只会启动一个恢复循环。
func enterTwitterRecovering(reason string) {
	if !twitterRecovering.CompareAndSwap(false, true) {
		return
	}
	twitterRecoveringSince.Store(time.Now().Unix())
	logger.Errorf("Twitter进入自动恢复模式：%v", reason)
	notifyTwitterLoginExpired(reason)
	go twitterRecoveryLoop()
}

func twitterRecoveryLoop() {
	for attempt := 0; ; attempt++ {
		delay := twitterRecoveryBackoff[len(twitterRecoveryBackoff)-1]
		if attempt < len(twitterRecoveryBackoff) {
			delay = twitterRecoveryBackoff[attempt]
		}
		time.Sleep(delay)

		if _, _, err := twitterVerifyFunc(); err != nil {
			logger.Warnf("Twitter自动恢复第%d次重试失败: %v", attempt+1, err)
			continue
		}
		downtime := time.Since(time.Unix(twitterRecoveringSince.Load(), 0))
		twitterRecovering.Store(false)
		notifyTwitterLoginRecovered(downtime)
		logger.Infof("Twitter已自动恢复，本次中断时长 %v", downtime)
		return
	}
}
