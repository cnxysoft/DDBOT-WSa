package mmsg

import "github.com/cnxysoft/DDBOT-WSa/adapter"

type CutElement struct {
}

func (c *CutElement) Type() adapter.ElementType {
	return Cut
}

func (c *CutElement) PackToElement(Target) adapter.IMessageElement {
	return nil
}

func (c *CutElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{c}}
}
