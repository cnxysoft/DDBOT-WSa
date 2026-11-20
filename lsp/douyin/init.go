package douyin

import (
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"net/http/cookiejar"
	"strings"
	"sync/atomic"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"

type runtimeCredentials struct {
	userAgent, signature, nonce, sessionID string
	jar                                    *cookiejar.Jar
}

var activeCredentials atomic.Pointer[runtimeCredentials]

func currentCredentials() *runtimeCredentials {
	if state := activeCredentials.Load(); state != nil {
		return state
	}
	return &runtimeCredentials{userAgent: defaultUserAgent}
}

func (s *runtimeCredentials) ready() bool { return s.signature != "" && s.nonce != "" }

func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
}

func setCookies() {
	prepared, err := credentialsFromSnapshot(config.GlobalConfig.Snapshot())
	if err != nil {
		logger.Warnf("抖音凭据无效，等待配置更新: %v", err)
		return
	}
	activeCredentials.Store(prepared)
}

func credentialsFromSnapshot(s *config.Snapshot) (*runtimeCredentials, error) {
	state := &runtimeCredentials{
		userAgent: s.GetString("douyin.userAgent"), signature: s.GetString("douyin.acSignature"),
		nonce: s.GetString("douyin.acNonce"), sessionID: s.GetString("douyin.sessionId"),
	}
	if state.userAgent == "" {
		state.userAgent = defaultUserAgent
	}
	for _, value := range []string{state.userAgent, state.signature, state.nonce, state.sessionID} {
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("抖音凭据和 UserAgent 不得包含换行")
		}
	}
	if (state.signature == "") != (state.nonce == "") {
		return nil, fmt.Errorf("douyin.acSignature 和 acNonce 必须同时设置或清空")
	}
	state.jar, _ = cookiejar.New(nil)
	return state, nil
}

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	keys := []string{"douyin.userAgent", "douyin.acSignature", "douyin.acNonce", "douyin.sessionId"}
	changed := false
	for _, key := range keys {
		if previous.GetString(key) != next.GetString(key) {
			changed = true
			break
		}
	}
	if !changed {
		return nil, nil, nil
	}
	state, err := credentialsFromSnapshot(next)
	if err != nil {
		return nil, nil, err
	}
	return func() { activeCredentials.Store(state) }, nil, nil
}

func DPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return BasePath[path] + path
	} else {
		return BasePath[path] + "/" + path
	}
}
