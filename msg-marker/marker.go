package msg_marker

import (
	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	"sync"
)

func init() {
	instance = new(marker)
	bot.RegisterModule(instance)
}

const moduleId = "sora233.message-read-marker"

type marker struct{}

var instance *marker

var logger = utils.GetModuleLogger(moduleId)

func (m *marker) MiraiGoModule() bot.ModuleInfo {
	return bot.ModuleInfo{
		ID:       moduleId,
		Instance: instance,
	}
}

func (m *marker) Init() {
}

func (m *marker) PostInit() {
}

func (m *marker) Serve(bot *bot.Bot) {
	if config.GlobalConfig.GetBool("message-marker.disable") {
		logger.Debug("自动已读被禁用")
		return
	}
	logger.Debug("自动已读已开启 (适配器模式)")
	// 适配器模式下暂时不支持自动已读功能
}

func (m *marker) Start(bot *bot.Bot) {
}

func (m *marker) Stop(bot *bot.Bot, wg *sync.WaitGroup) {
	wg.Done()
}
