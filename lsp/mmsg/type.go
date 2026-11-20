package mmsg

import "github.com/cnxysoft/DDBOT-WSa/adapter"

const (
	ImageBytes adapter.ElementType = 10000 + iota
	Typed
	Cut
	At
	Poke
	Video
	Record
	File
)

type CustomElement interface {
	PackToElement(target Target) adapter.IMessageElement
}
