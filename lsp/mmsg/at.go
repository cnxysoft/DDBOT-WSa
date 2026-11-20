package mmsg

import (
	"fmt"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

type AtElement struct {
	Target  int64
	Display string
}

func (a *AtElement) Type() adapter.ElementType {
	return At
}

func (a *AtElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{a}}
}

func (a *AtElement) PackToElement(target Target) adapter.IMessageElement {
	if a == nil {
		return nil
	}
	s := &adapter.AtSegment{
		Target:  a.Target,
		Display: a.Display,
	}
	if a.Target == 0 {
		if s.Display == "" {
			s.Display = "@全体成员"
		}
	} else {
		if s.Display == "" {
			if gi := localutils.GetBot().FindGroup(target.TargetCode()); gi != nil {
				if gmi := gi.FindMember(a.Target); gmi != nil {
					s.Display = fmt.Sprintf("@%v", gmi.DisplayName())
				}
			}
		}
		if s.Display == "" {
			s.Display = fmt.Sprintf("@%v", a.Target)
		}
	}
	return s
}

func NewAt(target int64, display ...string) *AtElement {
	var dis string
	if len(display) != 0 {
		dis = display[0]
	}
	return &AtElement{
		Target:  target,
		Display: dis,
	}
}
