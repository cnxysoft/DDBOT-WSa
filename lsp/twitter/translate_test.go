package twitter

import (
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/stretchr/testify/assert"
)

// TestStartAsyncTranslateDisabled 翻译未启用时不应创建预翻译通道，
// 推送渲染侧 WaitTranslation 直接返回 nil（不等待、不告警由 notify 侧负责）。
func TestStartAsyncTranslateDisabled(t *testing.T) {
	config.GlobalConfig.Set("twitter.translate.enabled", false)
	t.Cleanup(func() { config.GlobalConfig.Set("twitter.translate.enabled", false) })

	tweet := &Tweet{ID: "123", Content: "hello world this is english"}
	StartAsyncTranslate(tweet)

	assert.Nil(t, tweet.translationCh, "翻译未启用时不应创建 translationCh")
	assert.Nil(t, tweet.WaitTranslation(time.Millisecond), "无通道时 WaitTranslation 应返回 nil")
}

// TestStartAsyncTranslateEnabledCreatesChannel 翻译启用且内容非中文时应创建通道。
// TranslateTweet 的真实网络调用在 goroutine 内执行，测试只验证通道语义：
// WaitTranslation 应等待至超时（请求失败时通道收到 nil 结果同样会返回）。
func TestStartAsyncTranslateEnabledCreatesChannel(t *testing.T) {
	config.GlobalConfig.Set("twitter.translate.enabled", true)
	t.Cleanup(func() { config.GlobalConfig.Set("twitter.translate.enabled", false) })

	tweet := &Tweet{ID: "456", Content: "こんにちは世界 japanese content"}
	StartAsyncTranslate(tweet)

	assert.NotNil(t, tweet.translationCh, "翻译启用且非中文内容应创建 translationCh")

	// 中文占比超过阈值的内容不应触发翻译
	cn := &Tweet{ID: "789", Content: "这是一条全中文的推文内容不需要翻译"}
	StartAsyncTranslate(cn)
	assert.Nil(t, cn.translationCh, "中文内容不应创建 translationCh")
}

// TestWaitTranslationTimeout 通道存在但结果未就绪时，WaitTranslation 应在超时后返回 nil。
func TestWaitTranslationTimeout(t *testing.T) {
	tweet := &Tweet{ID: "t", translationCh: make(chan *TranslationResult, 1)}
	start := time.Now()
	assert.Nil(t, tweet.WaitTranslation(30*time.Millisecond))
	assert.Less(t, time.Since(start), time.Second, "应在超时后及时返回")
}
