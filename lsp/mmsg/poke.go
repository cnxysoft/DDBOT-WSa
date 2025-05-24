package mmsg

import (
	"github.com/Mrs4s/MiraiGo/message"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

// PokeElement 戳一戳
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
	switch target.TargetType() {
	case TargetGroup:
		gi := localutils.GetBot().FindGroup(target.TargetCode())
		if gi == nil {
			return nil
		}
		fi := gi.FindMember(p.Uin)
		if fi == nil {
			return nil
		}
		fi.Poke()
	case TargetPrivate:
		fi := localutils.GetBot().FindFriend(target.TargetCode())
		if fi == nil {
			return nil
		}
		fi.Poke()
	}
	return nil
}
