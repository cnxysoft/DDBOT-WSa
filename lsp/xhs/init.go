package xhs

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

var registeredConcern *Concern

func init() {
	registeredConcern = NewConcern(concern.GetNotifyChan())
	concern.RegisterConcern(registeredConcern)
}
