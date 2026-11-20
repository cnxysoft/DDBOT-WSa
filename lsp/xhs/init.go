package xhs

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

func init() {
	c := NewConcern(concern.GetNotifyChan())
	concern.RegisterConcern(c)
}
