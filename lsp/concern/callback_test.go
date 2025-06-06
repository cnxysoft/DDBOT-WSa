package concern

import (
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
	"testing"
)

type testNotify struct {
}

func (t *testNotify) Site() string {
	return "test"
}

func (t *testNotify) Type() concern_type.Type {
	return "test"
}

func (t *testNotify) GetUid() interface{} {
	return ""
}

func (t *testNotify) Logger() *logrus.Entry {
	return logrus.WithField("Site", t.Site())
}

func (t *testNotify) GetGroupCode() int64 {
	return test.G1
}

func (t *testNotify) ToMessage() *mmsg.MSG {
	return mmsg.NewMSG()
}

func TestDefaultCallback(t *testing.T) {
	var d DefaultCallback
	d.NotifyBeforeCallback(nil)
	d.NotifyAfterCallback(nil, nil)
	d.NotifyBeforeCallback(new(testNotify))
	d.NotifyAfterCallback(new(testNotify), nil)
}
