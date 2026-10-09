package permission

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/buntdb"
)

var logger = utils.GetModuleLogger("permission")

type StateManager struct {
	*localdb.ShortCut
	*KeySet
}

// CheckBlockList return true if blocked
func (c *StateManager) CheckBlockList(caller int64) bool {
	return c.Exist(c.BlockListKey(caller))
}

func (c *StateManager) AddBlockList(caller int64, d time.Duration) error {
	err := c.Set(c.BlockListKey(caller), "", localdb.SetExpireOpt(d), localdb.SetNoOverWriteOpt())
	if localdb.IsRollback(err) {
		err = localdb.ErrKeyExist
	}
	return err
}

func (c *StateManager) DeleteBlockList(caller int64) error {
	_, err := c.Delete(c.BlockListKey(caller))
	return err
}

// CheckGroupBlocked 判断群是否处于屏蔽状态。
// 显式加入 BlockList 的群一定被屏蔽；否则当配置 bot.defaultBlockAllGroups 为 true 时，
// 未出现在 GroupUnblocked 放行名单里的群都视为屏蔽。
func (c *StateManager) CheckGroupBlocked(groupCode int64) bool {
	if groupCode <= 0 {
		return false
	}
	if c.CheckBlockList(groupCode) {
		return true
	}
	if !cfg.GetDefaultBlockAllGroups() {
		return false
	}
	return !c.Exist(c.GroupUnblockedKey(groupCode))
}

// BlockGroup 屏蔽群，d 为屏蔽时长（0 表示永久）。
func (c *StateManager) BlockGroup(groupCode int64, d time.Duration) error {
	return c.AddBlockList(groupCode, d)
}

// UnblockGroup 解除群屏蔽：清除显式 BlockList 记录并把群加入放行名单。
// 群不处于屏蔽状态时返回 buntdb.ErrNotFound。
func (c *StateManager) UnblockGroup(groupCode int64) error {
	if !c.CheckGroupBlocked(groupCode) {
		return buntdb.ErrNotFound
	}
	_, err := c.Delete(c.BlockListKey(groupCode), localdb.IgnoreNotFoundOpt())
	if err != nil {
		return err
	}
	return c.Set(c.GroupUnblockedKey(groupCode), "")
}

func (c *StateManager) CheckRole(caller int64, role RoleType) bool {
	if role.String() == "" {
		return false
	}
	return c.Exist(c.PermissionKey(caller, role.String()))
}

func (c *StateManager) CheckAdmin(caller int64) bool {
	return c.CheckRole(caller, Admin)
}

func (c *StateManager) CheckGroupAdmin(groupCode int64, caller int64) bool {
	return c.CheckGroupRole(groupCode, caller, GroupAdmin)
}

func (c *StateManager) CheckGroupRole(groupCode int64, caller int64, role RoleType) bool {
	if role.String() == "" {
		return false
	}
	return c.Exist(c.GroupPermissionKey(groupCode, caller, role.String()))
}

func (c *StateManager) EnableGroupCommand(groupCode int64, command string) error {
	if c.CheckGlobalCommandDisabled(command) {
		return ErrGlobalDisabled
	}
	return c.operatorEnableKey(c.GroupEnabledKey(groupCode, command), Enable)
}

func (c *StateManager) DisableGroupCommand(groupCode int64, command string) error {
	if c.CheckGlobalCommandDisabled(command) {
		return ErrGlobalDisabled
	}
	return c.operatorEnableKey(c.GroupEnabledKey(groupCode, command), Disable)
}

func (c *StateManager) GlobalEnableGroupCommand(command string) error {
	return c.operatorEnableKey(c.GlobalEnabledKey(command), Enable)
}

func (c *StateManager) GlobalDisableGroupCommand(command string) error {
	return c.operatorEnableKey(c.GlobalEnabledKey(command), Disable)
}

func (c *StateManager) operatorEnableKey(key string, status string) error {
	var prev string
	var isOverwrite bool
	err := c.Set(key, status, localdb.SetGetIsOverwriteOpt(&isOverwrite), localdb.SetGetPreviousValueStringOpt(&prev))
	if err != nil {
		return err
	}
	if isOverwrite && prev == status {
		return ErrPermissionExist
	}
	return nil
}

// CheckGroupCommandEnabled check global first, check explicit enabled, must exist
func (c *StateManager) CheckGroupCommandEnabled(groupCode int64, command string) bool {
	var result bool
	_ = c.RCover(func() error {
		result = !c.CheckGlobalCommandDisabled(command) &&
			c.CheckGroupCommandFunc(groupCode, command, func(val string, exist bool) bool {
				return exist && val == Enable
			})
		return nil
	})
	return result
}

// CheckGroupCommandDisabled check global first, then check explicit disabled, must exist
func (c *StateManager) CheckGroupCommandDisabled(groupCode int64, command string) bool {
	var result bool
	_ = c.RCover(func() error {
		result = c.CheckGlobalCommandDisabled(command) ||
			c.CheckGroupCommandFunc(groupCode, command, func(val string, exist bool) bool {
				return exist && val == Disable
			})
		return nil
	})
	return result
}

func (c *StateManager) CheckGlobalCommandDisabled(command string) bool {
	return c.CheckGlobalCommandFunc(command, func(val string, exist bool) bool {
		return exist && val == Disable
	})
}

func (c *StateManager) CheckGroupCommandFunc(groupCode int64, command string, f func(val string, exist bool) bool) bool {
	var result bool
	err := c.RCoverTx(func(tx *buntdb.Tx) error {
		val, err := c.Get(c.GroupEnabledKey(groupCode, command))
		if err != nil && !localdb.IsNotFound(err) {
			return err
		}
		result = f(val, err == nil)
		return nil
	})
	if err != nil {
		logger.WithFields(localutils.GroupLogFields(groupCode)).
			WithField("command", command).
			Errorf("check group enable err %v", err)
		result = false
	}
	return result
}

func (c *StateManager) CheckGlobalCommandFunc(command string, f func(val string, exist bool) bool) bool {
	var result bool
	err := c.RCoverTx(func(tx *buntdb.Tx) error {
		val, err := c.Get(c.GlobalEnabledKey(command))
		if err != nil && !localdb.IsNotFound(err) {
			return err
		}
		result = f(val, err == nil)
		return nil
	})
	if err != nil {
		logger.WithField("command", command).
			Errorf("check global enable err %v", err)
		result = false
	}
	return result
}

func (c *StateManager) CheckGlobalSilence() bool {
	return c.Exist(c.GlobalSilenceKey())
}

func (c *StateManager) GlobalSilence() error {
	return c.Set(c.GlobalSilenceKey(), "")
}

func (c *StateManager) UndoGlobalSilence() error {
	_, err := c.Delete(c.GlobalSilenceKey(), localdb.IgnoreNotFoundOpt())
	return err
}

func (c *StateManager) CheckGroupSilence(groupCode int64) bool {
	return c.CheckGlobalSilence() || c.Exist(c.GroupSilenceKey(groupCode))
}

func (c *StateManager) GroupSilence(groupCode int64) error {
	if c.CheckGlobalSilence() {
		return ErrGlobalSilenced
	}
	return c.Set(c.GroupSilenceKey(groupCode), "")
}

func (c *StateManager) UndoGroupSilence(groupCode int64) error {
	if c.CheckGlobalSilence() {
		return ErrGlobalSilenced
	}
	_, err := c.Delete(c.GroupSilenceKey(groupCode), localdb.IgnoreNotFoundOpt())
	return err
}

func (c *StateManager) CheckGroupAdministrator(groupCode int64, caller int64) bool {
	log := logger.WithFields(logrus.Fields{
		"GroupCode": groupCode,
		"Caller":    caller,
	})
	if groupCode <= 0 || caller <= 0 {
		// 成品 x64 0xb8ba60：先判 groupCode/caller 有效性，无效直接 Error 返回 false。
		log.Error("invalid group or member id")
		return false
	}
	// 成品 x64 0xb8ba60 callees 含 utils.(*HackedBot).EnsureGroup（+0x200），
	// 不是 FindGroup；EnsureGroup 缺失时会实时拉取群资料。
	groupInfo, err := localutils.GetBot().EnsureGroup(groupCode)
	log.Debugf("获取到的groupInfo: %v\n", groupInfo)
	if err != nil || groupInfo == nil {
		if err != nil {
			// 成品 x64 0xb8bcdc：WithField("error", err) + Error("query group info failed")。
			log = log.WithField("error", err)
			log.Error("query group info failed")
		}
		log.Errorf("nil group info")
		return false
	}
	log = log.WithField("GroupName", groupInfo.Name)
	// 成品 x64 0xb8cda0 callees 含 RefreshGroupMember（+0x4f6），
	// 强制刷新群成员资料后再判断角色，而不是读本地缓存 FindMember。
	groupMemberInfo, err := localutils.GetBot().RefreshGroupMember(groupCode, caller)
	if err != nil || groupMemberInfo == nil {
		if err != nil {
			log = log.WithField("error", err)
			log.Error("realtime member role query failed")
		}
		log.Errorf("nil member info")
		return false
	}
	return groupMemberInfo.IsAdminOrOwner()
	//return true
}

// func (c *StateManager) CheckGroupAdministrator(groupCode int64, caller int64) bool {
// 	log := logger.WithFields(logrus.Fields{
// 		"GroupCode": groupCode,
// 		"Caller":    caller,
// 	})
// 	log.Info("正在admin.txt检查权限,一行一个\n")
// 	// 打开并读取admin.txt
// 	data, err := ioutil.ReadFile("admin.txt")
// 	if err != nil {
// 		// 如果有任何错误，比如文件不存在，我们可以直接返回false或输出错误日志
// 		log.Printf("Error reading admin.txt: %v", err)
// 		return false
// 	}

// 	// 将文件内容按行拆分
// 	lines := strings.Split(string(data), "\n")

// 	// 检查每一行
// 	for _, line := range lines {
// 		line = strings.TrimSpace(line) // 清除可能的空格和换行符
// 		if strconv.FormatInt(caller, 10) == line {
// 			return true
// 		}
// 	}
// 	return false
// }

func (c *StateManager) CheckGroupCommandPermission(groupCode int64, caller int64, command string) bool {
	return c.CheckRole(caller, Admin) || c.Exist(c.PermissionKey(groupCode, caller, command))
}

func (c *StateManager) GrantRole(target int64, role RoleType) error {
	if role.String() == "" {
		return errors.New("error role")
	}
	err := c.Set(c.PermissionKey(target, role.String()), "", localdb.SetNoOverWriteOpt())
	if localdb.IsRollback(err) {
		return ErrPermissionExist
	}
	return err
}

func (c *StateManager) UngrantRole(target int64, role RoleType) error {
	if role.String() == "" {
		return errors.New("error role")
	}
	_, err := c.Delete(c.PermissionKey(target, role.String()))
	if localdb.IsNotFound(err) {
		return ErrPermissionNotExist
	}
	return err
}

func (c *StateManager) CheckNoAdmin() bool {
	return len(c.ListAdmin()) == 0
}

func (c *StateManager) ListAdmin() []int64 {
	var result []int64
	err := c.RCoverTx(func(tx *buntdb.Tx) error {
		return tx.Ascend(c.PermissionKey(), func(key, value string) bool {
			splits := strings.Split(key, ":")
			if len(splits) != 3 {
				return true
			}
			if NewRoleFromString(splits[2]) == Admin {
				i, err := strconv.ParseInt(splits[1], 0, 64)
				if err != nil {
					logger.WithField("Key", key).Errorf("Parse PermissionKey error %v", err)
				} else {
					result = append(result, i)
				}
			}
			return true
		})
	})
	if err != nil {
		result = nil
		logger.Errorf("ListAdmin error %v", err)
	}
	return result
}

func (c *StateManager) ListGroupAdmin(groupCode int64) []int64 {
	var result []int64
	err := c.RCoverTx(func(tx *buntdb.Tx) error {
		return tx.Ascend(c.GroupPermissionKey(groupCode), func(key, value string) bool {
			splits := strings.Split(key, ":")
			if len(splits) != 4 {
				return true
			}
			if NewRoleFromString(splits[3]) == GroupAdmin {
				i, err := strconv.ParseInt(splits[2], 0, 64)
				if err != nil {
					logger.WithField("Key", key).Errorf("Parse GroupPermissionKey error %v", err)
				} else {
					result = append(result, i)
				}
			}
			return true
		})
	})
	if err != nil {
		result = nil
		logger.Errorf("ListGroupAdmin error %v", err)
	}
	return result
}

func (c *StateManager) GrantGroupRole(groupCode int64, target int64, role RoleType) error {
	if role.String() == "" {
		return errors.New("error role")
	}
	err := c.Set(c.GroupPermissionKey(groupCode, target, role.String()), "", localdb.SetNoOverWriteOpt())
	if localdb.IsRollback(err) {
		return ErrPermissionExist
	}
	return err
}

func (c *StateManager) UngrantGroupRole(groupCode int64, target int64, role RoleType) error {
	if role.String() == "" {
		return errors.New("error role")
	}
	_, err := c.Delete(c.GroupPermissionKey(groupCode, target, role.String()))
	if localdb.IsNotFound(err) {
		return ErrPermissionNotExist
	}
	return err
}

func (c *StateManager) GrantPermission(groupCode int64, target int64, command string) error {
	if c.CheckGlobalCommandDisabled(command) {
		return ErrGlobalDisabled
	}
	err := c.Set(c.PermissionKey(groupCode, target, command), "", localdb.SetNoOverWriteOpt())
	if localdb.IsRollback(err) {
		return ErrPermissionExist
	}
	return err
}

func (c *StateManager) UngrantPermission(groupCode int64, target int64, command string) error {
	if c.CheckGlobalCommandDisabled(command) {
		return ErrGlobalDisabled
	}
	_, err := c.Delete(c.PermissionKey(groupCode, target, command))
	if localdb.IsNotFound(err) {
		return ErrPermissionNotExist
	}
	return err
}

func (c *StateManager) RequireAny(option ...RequireOption) bool {
	for _, opt := range option {
		if opt.Validate(c) {
			return true
		}
	}
	return false
}

func (c *StateManager) RemoveAllByGroupCode(groupCode int64) ([]string, error) {
	var indexKey = []string{
		c.GroupPermissionKey(),
		c.PermissionKey(),
		c.GroupEnabledKey(),
		c.GroupUnblockedKey(),
	}
	var prefixKey = []string{
		c.GroupPermissionKey(groupCode),
		c.PermissionKey(groupCode),
		c.GroupEnabledKey(groupCode),
		c.GroupUnblockedKey(groupCode),
	}
	return localdb.RemoveByPrefixAndIndex(prefixKey, indexKey)
}

func (c *StateManager) FreshIndex() {
	// Build indices for the namespace this StateManager is configured with
	for _, pattern := range []localdb.KeyPatternFunc{c.PermissionKey, c.GroupPermissionKey, c.GroupEnabledKey, c.GroupUnblockedKey} {
		c.CreatePatternIndex(pattern, nil)
	}
	for _, group := range localutils.GetBot().GetGroupList() {
		c.CreatePatternIndex(c.GroupPermissionKey, []interface{}{group.Code})
		c.CreatePatternIndex(c.GroupEnabledKey, []interface{}{group.Code})
	}
}

func NewStateManager() *StateManager {
	sm := &StateManager{
		KeySet: NewKeySet(),
	}
	sm.FreshIndex()
	return sm
}

// NewTgStateManager returns a StateManager whose keys are in the TG namespace
func NewTgStateManager() *StateManager {
	sm := &StateManager{
		KeySet: NewKeySet().WithNamespace("tg"),
	}
	sm.FreshIndex()
	return sm
}
