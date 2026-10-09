package missevan

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

func init() {
	concern.RegisterConcern(NewConcern())
}
