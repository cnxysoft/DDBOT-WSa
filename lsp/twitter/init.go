package twitter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	BaseURL = []string{
		"https://nitter.tiekoetter.com/",
		"https://nitter.mizuki.st/",
		"https://nitter.ip6arpa.uk/",
		"https://nitter.catsarch.com/",
		"https://nitter.jaydenha.uk/",
		"https://nitter.click/",
	}
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
	runtimeGen := twitterSnapshot()
	return runtimeGen.fetchMode != APIFetchModePerUser
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
	r, err := newTwitterRuntime(config.GlobalConfig.Snapshot())
	if err != nil {
		logger.WithError(err).Warn("Twitter 配置无效，保持旧会话")
		return
	}
	twitterStateMu.Lock()
	liveTwitter.Store(r)
	twitterStateMu.Unlock()
	if r.api != nil && r.api.IsEnabled() {
		if _, _, err := verifyTwitterAPI(); err != nil {
			enterTwitterRecovering("Twitter Cookie 验证失败")
		}
	}
}

func IsTwitterEnabled() bool {
	runtimeGen := twitterSnapshot()
	twitterAPI := runtimeGen.api
	return runtimeGen.mode == ModeAPI && twitterAPI != nil && twitterAPI.IsEnabled() && !twitterRecovering.Load()
}

func IsMirrorMode() bool {
	runtimeGen := twitterSnapshot()
	return runtimeGen.mode == ModeMirror
}

// resolveTwitterMode 解析twitter.mode配置。
// 默认使用免账号的镜像模式，仅显式配置 api 时启用账号 API。
func resolveTwitterMode(mode string) string {
	if mode == ModeAPI {
		return ModeAPI
	}
	return ModeMirror
}

// verifyTwitterAPI 单次验证Cookie：拉取账号信息并刷新queryId缓存。
// 验证成功会就地设置twitterAPI的screenName和mainJSURL。
func verifyTwitterAPI() (screenName, mainJsUrl string, err error) {
	return verifyTwitterAPIFor(twitterSnapshot())
}

func verifyTwitterAPIFor(runtimeGen *twitterRuntime) (screenName, mainJsUrl string, err error) {
	twitterAPI := runtimeGen.api
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
	twitterAPI.setMainJS(mjUrl)
	// 获取 queryId（从 sw.js → LoggedInMain 提取缓存），失败不阻塞验证
	if err := twitterAPI.refreshAPIFromMainJS(false, mjUrl); err != nil {
		logger.Warnf("获取 queryId 失败，使用默认配置: %v", err)
	} else {
		logger.Infof("成功获取 queryId: %s", twitterAPI.GetQueryId())
	}
	return sn, mjUrl, nil
}

var (
	twitterRecovering      atomic.Bool
	twitterRecoveringSince atomic.Int64
	twitterRecoveryWorkers sync.WaitGroup
	twitterVerifyFunc      func() (string, string, error) // optional test verifier
	// twitterRecoveryBackoff 自动恢复重试的退避序列，超出后按最后一项封顶
	twitterRecoveryBackoff = []time.Duration{
		15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
		5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	}
)

// enterTwitterRecovering 进入自动恢复模式：暂停Twitter刷新、向管理员发一次告警，
// 并后台按退避节奏重试验证，成功后自动恢复推送并通知。重复调用只会启动一个恢复循环。
func enterTwitterRecovering(reason string) {
	enterTwitterRecoveringFor(liveTwitter.Load(), reason)
}

func enterTwitterRecoveringFor(generation *twitterRuntime, reason string) {
	twitterStateMu.Lock()
	if liveTwitter.Load() != generation || !twitterRecovering.CompareAndSwap(false, true) {
		twitterStateMu.Unlock()
		return
	}
	twitterRecoveringSince.Store(time.Now().Unix())
	twitterRecoveryWorkers.Add(1)
	twitterStateMu.Unlock()
	logger.Errorf("Twitter进入自动恢复模式：%v", reason)
	notifyTwitterLoginExpired(reason)
	verifier := twitterVerifyFunc
	if verifier == nil {
		runtimeGen := generation
		if runtimeGen == nil {
			runtimeGen = twitterSnapshot()
		}
		verifier = func() (string, string, error) { return verifyTwitterAPIFor(runtimeGen) }
	}
	backoff := append([]time.Duration(nil), twitterRecoveryBackoff...)
	go func() {
		defer twitterRecoveryWorkers.Done()
		twitterRecoveryLoopFor(generation, verifier, backoff)
	}()
}

func twitterRecoveryLoopFor(generation *twitterRuntime, verifier func() (string, string, error), backoff []time.Duration) {
	for attempt := 0; ; attempt++ {
		delay := backoff[len(backoff)-1]
		if attempt < len(backoff) {
			delay = backoff[attempt]
		}
		time.Sleep(delay)
		if liveTwitter.Load() != generation || !twitterRecovering.Load() {
			return
		}

		if _, _, err := verifier(); err != nil {
			logger.Warnf("Twitter自动恢复第%d次重试失败: %v", attempt+1, err)
			continue
		}
		twitterStateMu.Lock()
		if liveTwitter.Load() != generation {
			twitterStateMu.Unlock()
			return
		}
		downtime := time.Since(time.Unix(twitterRecoveringSince.Load(), 0))
		twitterRecovering.Store(false)
		twitterStateMu.Unlock()
		notifyTwitterLoginRecovered(downtime)
		logger.Infof("Twitter已自动恢复，本次中断时长 %v", downtime)
		return
	}
}
