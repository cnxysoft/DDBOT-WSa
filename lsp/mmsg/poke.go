package mmsg

import (
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

type PokeElement struct {
	Uin int64
}

func NewPoke(uin int64) *PokeElement {
	return &PokeElement{Uin: uin}
}

func (p *PokeElement) Type() message.ElementType {
	return Poke
}

func (p *PokeElement) PackToElement(target Target) message.IMessageElement {
	botInstance := localutils.GetBotInstance()
	if botInstance == nil {
		return nil
	}

	bot, ok := botInstance.(adapter.BotCaller)
	if !ok {
		return nil
	}

	switch target.TargetType() {
	case TargetGroup:
		groupCode := target.TargetCode()
		if groupCode == 0 {
			return nil
		}
		if err := bot.GroupPoke(groupCode, p.Uin); err != nil {
			return nil
		}
	case TargetPrivate:
		userId := target.TargetCode()
		if userId == 0 {
			return nil
		}
		if err := bot.FriendPoke(userId); err != nil {
			return nil
		}
	}
	return nil
}
