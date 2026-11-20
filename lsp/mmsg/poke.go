package mmsg

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

type PokeElement struct {
	Uin int64
}

func NewPoke(uin int64) *PokeElement {
	return &PokeElement{Uin: uin}
}

func (p *PokeElement) Type() adapter.ElementType {
	return Poke
}

func (p *PokeElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{p}}
}

func (p *PokeElement) PackToElement(target Target) adapter.IMessageElement {
	botInstance := utils.GetBotInstance()
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
