package bot

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	ob11 "github.com/cnxysoft/DDBOT-WSa/adapter/onebot-v11"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/cnxysoft/DDBOT-WSa/adapter/satori"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
)

func adapterBuilder(s *config.Snapshot) adapter.AdapterBuilder {
	settings := adapter.GetAdapterConfigFrom(s)
	qqSettings := qqofficial.GetConfigFrom(s)
	statePath := s.GetString("qqofficial.state-file")
	return func() (adapter.Adapter, error) {
		cfg := *settings
		switch adapter.AdapterType(settings.Mode) {
		case adapter.AdapterTypeQQOfficial:
			qq := *qqSettings
			return qqofficial.NewQQOfficialAdapterWithConfig(&qq, statePath), nil
		case adapter.AdapterTypeSatori:
			return satori.NewSatoriAdapter(&cfg), nil
		default:
			return ob11.NewOneBotAdapter(&cfg), nil
		}
	}
}

func PrepareAdapterReload(previous, next *config.Snapshot) (func(), func(), error) {
	if Instance == nil || Instance.Messenger == nil {
		return nil, nil, nil
	}
	changed := false
	sections := []string{"adapter"}
	if adapter.GetAdapterConfigFrom(next).Mode == string(adapter.AdapterTypeQQOfficial) {
		sections = append(sections, "qqofficial")
	} else {
		sections = append(sections, "websocket")
	}
	for _, section := range sections {
		changed = changed || !reflect.DeepEqual(previous.Get(section), next.Get(section))
	}
	if !changed {
		return nil, nil, nil
	}
	live, ok := Instance.Adapter.(*adapter.ReloadableAdapter)
	if !ok {
		return nil, nil, fmt.Errorf("adapter 生命周期尚未初始化")
	}
	name := adapter.GetAdapterConfigFrom(next).Mode
	identity := ""
	if name == string(adapter.AdapterTypeQQOfficial) {
		identity = qqofficial.IdentityPrefix + strings.TrimSpace(qqofficial.GetConfigFrom(next).AppID)
	}
	commit, abort, err := live.PrepareReload(name, identity, adapterBuilder(next))
	if err != nil || commit == nil {
		return commit, abort, err
	}
	instance := Instance
	return func() {
		commit()
		// Publishing can run callbacks that save config. Leave the transaction
		// goroutine first, and notify even when the reconnect fits between polls.
		go func() {
			if live.IsConnected() {
				instance.Messenger.Online.Store(true)
				instance.Messenger.Uin.Store(live.GetSelfID())
				instance.Uin.Store(instance.Messenger.GetSelfID())
				instance.Online.Store(true)
				eventbus.BusObj.Publish("bot_online", true)
			}
		}()
	}, abort, nil
}
