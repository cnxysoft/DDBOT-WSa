package cfg

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestHotPolicyMatchesSupportedConsumers(t *testing.T) {
	for _, key := range []string{
		"notify.parallel", "twitch.onlyOnlineNotify", "bot.commandPrefix", "customCommandPrefix.Help", "cronjob", "logLevel", "ffmpeg.logLevel",
		"platform.bilibili.enable", "bilibili.interval", "weibo.interval", "heybox.interval",
		"imageMerge.mode", "twitter.imageMerge.maxWidth", "bilibili.hiddenSub", "twitter.translate.enabled",
	} {
		if !IsHotKey(key) {
			t.Errorf("supported hot key rejected: %s", key)
		}
	}
	for _, key := range []string{
		"adapter.mode", "websocket.token", "admin.addr", "proxy.type", "extDb.path", "template.enable",
		"bilibili.SESSDATA", "twitter.mode", "telegram.enable", "imagePool.type",
		"unknown.setting", "twitter.imageMerge.unknown", "youtube.interval",
	} {
		if IsHotKey(key) {
			t.Errorf("resource or unsupported key incorrectly hot: %s", key)
		}
	}
}

func TestReloadValidationRejectsWeakTypeCoercions(t *testing.T) {
	c := &config.Config{Viper: viper.New()}
	c.ConfigureReload(ValidateReload, IsHotKey)
	for _, input := range []string{
		"bot:\n  commandPrefix: [oops]\n",
		"admin:\n  enable: maybe\n",
		"twitter:\n  auth_token: [oops]\n",
		"localProxyPool:\n  oversea: [false]\n",
		"notify:\n  parallel: true\n",
		"notify:\n  parallel: 1.5\n",
		"telegram:\n  chats: [true]\n",
		"platform:\n  bilibili:\n    enable: typo\n",
		"platform:\n  bilibili:\n    enable: {value: false}\n",
		"twitter:\n  interval: true\n",
		"twitter:\n  interval: [60]\n",
		"bilibili:\n  interval: ' 4s '\n",
		"ffmpeg:\n  logLevel: true\n",
		"bilibili:\n  hiddenSub: typo\n",
		"cronjob:\n  - cron: '0 * * * *'\n    target:\n      group: [true]\n",
		"cronjob:\n  - cron: '0 * * * *'\n    target:\n      group: [1.5]\n",
		"cronjob:\n  - cron: '0 * * * *'\n    templateName: true\n",
		"imageMerge:\n  mode: [always]\n",
		"bilibili:\n  imageMergeMode: [bad]\n",
		"imageMerge:\n  maxWidth: true\n",
	} {
		t.Run(input, func(t *testing.T) {
			if c.Validate([]byte(input)) == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestReloadValidationRetainsConsumerCompatibleValues(t *testing.T) {
	c := &config.Config{Viper: viper.New()}
	c.ConfigureReload(ValidateReload, IsHotKey)
	for _, input := range []string{
		"ffmpeg:\n  logLevel: ' WARN '\n",
		"twitter:\n  interval: 0.5\n",
		"twitter:\n  interval: '0.5'\n",
		"twitter:\n  interval: ' 60 '\n",
		"twitter:\n  interval: ' 2m '\n",
		"platform:\n  bilibili:\n    enable: 'false'\n",
		"imageMerge:\n  mode: true\n  maxWidth: 0\n  maxHeight: inherit\n",
		"imageMerge:\n  mode: false\n  layout: inherit\n",
		"bilibili:\n  imageMergeMode: ' ALWAYS '\n",
		"imageMerge:\n  maxWidth: ' 120 '\n",
		"debug:\n  group: [123456, '234567']\n",
		"cronjob:\n  - cron: '@every 1h'\n    target:\n      group: ['123456']\n      private: [-100123456]\n",
		"twitter:\n  interval: null\n",
		"bilibili:\n  interval: 0\n",
		"{}\n",
	} {
		t.Run(input, func(t *testing.T) {
			if err := c.Validate([]byte(input)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
