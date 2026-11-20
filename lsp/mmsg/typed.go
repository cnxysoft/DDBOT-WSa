package mmsg

import "github.com/cnxysoft/DDBOT-WSa/adapter"

// TypedElement 根据TargetType选择不同的element，不解决循环问题，使用不当可能导致堆栈溢出
// 可以同时设置 OnGroup 和 OnPrivate ，发送时会根据目标自动选择
// 如果只设置了一个，发送另一个时会返回 nil ，即这里什么也不发送
type TypedElement struct {
	privateE adapter.IMessageElement
	groupE   adapter.IMessageElement
}

func NewTypedElement() *TypedElement {
	return new(TypedElement)
}

func NewGroupElement(e adapter.IMessageElement) *TypedElement {
	return NewTypedElement().OnGroup(e)
}

func NewPrivateElement(e adapter.IMessageElement) *TypedElement {
	return NewTypedElement().OnPrivate(e)
}

func (t *TypedElement) Type() adapter.ElementType {
	return Typed
}

func (t *TypedElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{t}}
}

func (t *TypedElement) PackToElement(target Target) adapter.IMessageElement {
	if t.privateE == nil && t.groupE == nil {
		return nil
	}
	var e adapter.IMessageElement
	switch target.TargetType() {
	case TargetPrivate:
		e = t.privateE
	case TargetGroup:
		e = t.groupE
	}
	if e == nil {
		return e
	}
	if ce, ok := e.(CustomElement); ok {
		return ce.PackToElement(target)
	}
	return e
}

func (t *TypedElement) OnPrivate(e adapter.IMessageElement) *TypedElement {
	if t == e {
		panic("TypedElement can not type self")
	}
	t.privateE = e
	return t
}

func (t *TypedElement) OnGroup(e adapter.IMessageElement) *TypedElement {
	if t == e {
		panic("TypedElement can not type self")
	}
	t.groupE = e
	return t
}
