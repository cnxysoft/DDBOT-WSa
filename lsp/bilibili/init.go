package bilibili

import (
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

// 后台刷新启动闸门：init 起两个刷新协程，等待 Concern.Start 调用
// startBackgroundRefresh 放行后再工作。成品 init.go:15-38 结构：
//
//	init() -> RegisterConcern / initCookieJar / 两个 go func（各 <-通道 后 waitForBilibiliLogin）
var (
	// backgroundRefreshReady 只被关闭一次，唤醒后台刷新协程。
	backgroundRefreshReady = make(chan struct{})
	// backgroundRefreshOnce 保证放行动作只执行一次。
	backgroundRefreshOnce sync.Once
)

func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
	initCookieJar()
	go func() {
		<-backgroundRefreshReady
		waitForBilibiliLogin()
		refreshCookieJar()
		refreshNavWbi()
		for range time.Tick(time.Hour) {
			if bilibiliLoginReady() {
				refreshCookieJar()
			}
		}
	}()
	go func() {
		<-backgroundRefreshReady
		waitForBilibiliLogin()
		for range time.Tick(2 * time.Minute) {
			if bilibiliLoginReady() {
				refreshNavWbi()
			}
		}
	}()
}

// waitForBilibiliLogin 阻塞直到登录凭据就绪（每 5s 复查）。
// 成品 init.go:40（64B）无参：内部 time.Sleep(5s) 轮询 bilibiliLoginReady。
func waitForBilibiliLogin() {
	for !bilibiliLoginReady() {
		time.Sleep(5 * time.Second)
	}
}

// startBackgroundRefresh 放行后台刷新协程，只生效一次。
// 成品 init.go:46（64B）：sync.Once.Do(close(backgroundRefreshReady))。
func startBackgroundRefresh() {
	backgroundRefreshOnce.Do(func() {
		close(backgroundRefreshReady)
	})
}
