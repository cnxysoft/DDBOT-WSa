package lsp

import "github.com/Sora233/MiraiGo-Template/config"

func (l *Lsp) PrepareImageReload(previous, next *config.Snapshot) (func(), func(), error) {
	return PrepareImagePoolReload(previous, next, l.pool)
}

func (l *Lsp) CloseResources() {
	if l.pool != nil {
		_ = l.pool.Close()
	}
}
