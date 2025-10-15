package permission

import (
    localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
)

// Namespace support: allow separating QQ and TG permission stores via key base name.
// ns == ""  => QQ (default), keys like Permission:..., GroupPermission:...
// ns == "tg" => TG, keys like TgPermission:..., TgGroupPermission:...

type KeySet struct{ ns string }

func (k *KeySet) PermissionKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("Permission"), keys)
}

func (k *KeySet) GroupPermissionKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("GroupPermission"), keys)
}

func (k *KeySet) GroupEnabledKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("GroupEnable"), keys)
}

func (k *KeySet) GlobalEnabledKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("GlobalEnable"), keys)
}

func (k *KeySet) GroupSilenceKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("GroupSilence"), keys)
}

func (k *KeySet) GlobalSilenceKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("GlobalSilence"), keys)
}

func (k *KeySet) BlockListKey(keys ...interface{}) string {
    return localdb.NamedKey(k.base("BlockList"), keys)
}

func (k *KeySet) base(name string) string {
    if k != nil && k.ns == "tg" {
        return "Tg" + name
    }
    return name
}

func NewKeySet() *KeySet { return &KeySet{} }

// WithNamespace returns a copy of KeySet that uses a fixed namespace.
func (k *KeySet) WithNamespace(ns string) *KeySet { return &KeySet{ns: ns} }
