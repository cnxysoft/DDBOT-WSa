package douyu

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
}
