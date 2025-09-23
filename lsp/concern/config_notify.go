package concern

import "github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"

// GroupConcernNotifyConfig 推送配置
type GroupConcernNotifyConfig struct {
	TitleChangeNotify concern_type.Type `json:"title_change_notify"`
	OfflineNotify     concern_type.Type `json:"offline_notify"`
	ExtendNotify      concern_type.Type `json:"extend_notify"`
}

func (g *GroupConcernNotifyConfig) CheckTitleChangeNotify(ctype concern_type.Type) bool {
	return g.TitleChangeNotify.ContainAll(ctype)
}

func (g *GroupConcernNotifyConfig) CheckOfflineNotify(ctype concern_type.Type) bool {
	return g.OfflineNotify.ContainAll(ctype)
}

func (g *GroupConcernNotifyConfig) CheckExtendNotify(ctype concern_type.Type) bool {
	return g.ExtendNotify.ContainAll(ctype)
}
