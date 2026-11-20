package weibo

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
)

func TestConcernTypesExcludesCookieAlert(t *testing.T) {
	c := NewConcern(nil)

	types := c.Types()

	if len(types) != 1 || types[0] != News {
		t.Fatalf("Types() = %v, want [%s]", types, News)
	}
	for _, typ := range types {
		if typ == CookieAlert {
			t.Fatalf("Types() contains internal alert type %s", CookieAlert)
		}
	}
}

func TestNotifyGeneratorHandlesCookieAlert(t *testing.T) {
	c := NewConcern(nil)
	alert := NewCookieAlertNotify(12345, false)

	notifies := c.notifyGenerator()(12345, alert)

	if len(notifies) != 1 {
		t.Fatalf("notifyGenerator() produced %d notifications, want 1", len(notifies))
	}
	if notifies[0].Type() != concern_type.Type(CookieAlert) {
		t.Fatalf("notify type = %s, want %s", notifies[0].Type(), CookieAlert)
	}
	if notifies[0].GetGroupCode() != 12345 {
		t.Fatalf("notify group = %d, want 12345", notifies[0].GetGroupCode())
	}
}
