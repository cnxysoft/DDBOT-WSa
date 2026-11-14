package qqofficial

// 本文件给 IDMap 提供一个**零依赖**的 Persister：单文件 JSON + 原子写。
//
// 顺带把「机器人所在群清单」也存进同一个文件：官方没有「列出我加入的群」这个接口
// （接口索引里就没有，只有 /v2/groups/{openid}/info 这种必须先知情的接口），群清单只能
// 靠 GROUP_ADD_ROBOT / GROUP_DEL_ROBOT 事件自建；不落盘的话每次重启都得等群里有动静
// 才认得出来，而 lsp 启动流程又必须等到群列表加载完成。
//
// 文件结构（两段互不覆盖：Save 只换 idmap 段，SaveGroups 只换 groups 段）：
//
//	{"version":1,"app_id":"1900000000","updated_at":"…","idmap":{"<appID>\x1f<openid>":1},"groups":[…]}
//
// 这个文件丢了就等于内部 ID 丢了（lsp 的群权限与订阅按内部 ID 存在 .lsp.db 里，
// 编号从 1 重排会把旧权限与订阅静默发给别的群），所以每次写盘都会把上一版轮转成
// 同目录的 <路径>.bak，读盘发现主文件不可用时先从备份恢复并打醒目告警 —— 见 readLocked。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// ConfigKeyStateFile 是状态文件路径的配置键（qqofficial.state-file）。
	ConfigKeyStateFile = "state-file"
	// LegacyStateFile 是**升级前**的默认状态文件名：进程工作目录下的点文件，不带 appId。
	//
	// 现在只为一件事留着它：一次性迁移（见 MigrateLegacyStateFile）。新名字见
	// DefaultStateFilePath —— 为什么必须带 appId：openid 是 per-bot 的（同一个群，
	// 个人机器人看到 0F6E696A…，企业机器人看到 B97B93A3…），换机器人之后旧文件里的
	// 映射与旧群对新机器人全部失效；不带 appId 的文件名会让新机器人直接接着用旧文件，
	// 旧群仍被当成本机器人的群、旧 openid 仍占着内部 ID。
	LegacyStateFile = ".qqofficial-state.json"
	// defaultStateFileFormat 是带 appId 的默认状态文件名格式。
	defaultStateFileFormat = ".qqofficial-state-%s.json"
	// stateFileMigratedSuffix 是迁移成功后旧文件改成的名字（旧文件名 + 这个后缀）。
	//
	// 为什么不直接删掉：迁移是自动化判断，万一判错（用户其实想换回旧机器人，或者
	// 把状态文件手动挪了回来），改个名就能还原；删除不可逆，而内部 ID 丢了就等于
	// 群权限与订阅全部错位到别的群。
	stateFileMigratedSuffix = ".migrated"
	// stateFileVersion 是文件格式版本。
	stateFileVersion = 1
	// stateFileMode 是状态文件权限。里面有 openid 映射，只给本用户读写。
	stateFileMode = 0o600
	// stateFileBackupSuffix 是备份文件后缀：备份放在状态文件同目录、同名前缀下
	// （".qqofficial-state.json" → ".qqofficial-state.json.bak"）。
	stateFileBackupSuffix = ".bak"
)

// GroupRecord 是一条「机器人所在群」的记录。
//
// 官方没有群列表接口，所以这份记录就是 GetGroupList 的全部依据；InfoKnown 表示
// 群名/人数是否已经从 GET /v2/groups/{openid}/info 取到过（该接口实测可用，
// 但文档把它标成 11253 白名单，所以取不到时保留记录、只把名字留空）。
type GroupRecord struct {
	OpenID         string `json:"open_id"`
	Name           string `json:"name,omitempty"`
	MemberCount    int    `json:"member_count,omitempty"`
	MaxMemberCount int    `json:"max_member_count,omitempty"`
	AddedAt        int64  `json:"added_at,omitempty"`
	InfoKnown      bool   `json:"info_known,omitempty"`
}

// stateFile 是磁盘上的完整状态。
type stateFile struct {
	Version   int              `json:"version"`
	AppID     string           `json:"app_id,omitempty"`
	UpdatedAt string           `json:"updated_at,omitempty"`
	IDMap     map[string]int64 `json:"idmap"`
	Groups    []GroupRecord    `json:"groups"`
}

// JSONStore 是 IDMap 的 Persister 实现（也是群清单的落盘点）。
//
// 每次读写都直接碰文件，不做内存缓存：调用点是「新映射分配后定期 Flush」和启动时
// Load，频率极低，缓存换来的只是不一致风险。写法是先写同目录临时文件、fsync、再
// rename——中途断电要么是旧文件，要么是新文件，不会出现半个 JSON。
//
// 零新增依赖：只用标准库。
type JSONStore struct {
	path  string
	appID string
	mu    sync.Mutex

	// 下面两个是「喊过一次就够了」的开关，只在持 s.mu 的读盘路径里读写。
	firstRunWarned bool   // 已经提示过「状态文件没了，内部 ID 会重排」
	appIDWarned    string // 已经提示过不一致的那个「文件里的 appID」
}

// DefaultStateFilePath 返回「按当前 appId 命名」的默认状态文件路径。
//
// 暂时性说明（2026-11 定）：这是基于「openid per-bot」这个当前平台行为做的命名，
// 依据是实测（同一个群在个人/企业机器人下 openid 完全不同，拿旧 openid 请求会得到
// 11255）。如果哪天腾讯改成跨机器人稳定的 openid，带 appId 的文件名就没有必要了，
// 应当重新评估（那时换机器人可以让新旧机器人共享映射，迁移方向要反过来设计）。
//
// appId 为空（配置还没填全）时退回旧名字：那种情况下 Config.Validate 会先报缺
// app-id，这里的返回值只是别让路径变成 ".qqofficial-state-.json" 这种怪东西。
func DefaultStateFilePath(appID string) string {
	if suffix := appIDSuffix(appID); suffix != "" {
		return fmt.Sprintf(defaultStateFileFormat, suffix)
	}
	return LegacyStateFile
}

// StateFilePath 返回本次运行使用的状态文件路径。
//
// 用户显式配了 qqofficial.state-file 就**完全按用户的路径走**：不改名、不迁移。
// 那条路径是用户自己挑的（可能被容器挂载、被运维脚本或备份任务引用），改名会让
// 升级上来的人找不到自己的文件。只有默认路径才会用带 appId 的命名。
func StateFilePath(appID string) string {
	if p := getString(ConfigKeyStateFile); p != "" {
		return p
	}
	return DefaultStateFilePath(appID)
}

// StateFilePathConfigured 报告用户是否显式配置了 qqofficial.state-file。
//
// 迁移只该发生在默认路径上（见 StateFilePath 的说明），调用方据此分支。
func StateFilePathConfigured() bool {
	return getString(ConfigKeyStateFile) != ""
}

// appIDSuffix 把 appId 收敛成可以安全拼进文件名的片段。
//
// 正常 appId 是纯数字，但它是从配置里读来的字符串：一旦混进 "/" 或 ".."，
// 直接拼进文件名就会把状态文件写到目录之外。这里按白名单逐个字符过滤，
// 非法字符统一换成 "_"（而不是报错 —— 名字丑一点也比写错位置安全）。
func appIDSuffix(appID string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(appID) {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// NewJSONStore 建一个文件存储。目录不存在时会创建（0700）。
func NewJSONStore(path, appID string) (*JSONStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%s: 状态文件路径为空", ConfigKeyPrefix)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("%s: 创建状态文件目录 %s 失败：%w", ConfigKeyPrefix, dir, err)
		}
	}
	return &JSONStore{path: path, appID: appID}, nil
}

// Path 返回状态文件路径。
func (s *JSONStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// readLocked 读主状态文件；主文件不可用时先尝试从备份恢复。调用方必须持有 s.mu。
//
// 三种情况分开对待（日志级别不同，调用方拿到的结果也不同）：
//  1. 主文件与备份都不存在（或都为空）—— 真·首次运行：返回空状态 + Warn，但不是错误；
//  2. 主文件不可用、备份可用 —— 返回备份的内容 + 醒目告警（丢了主文件是 Warn，
//     解析失败说明文件真坏了是 Error），备份就是「丢了也能把 ID 认回来」的兜底；
//  3. 主文件解析失败且没有可用备份 —— 返回错误（Error 级日志）。
//
// 第 3 种绝不能「当作空的」：当作空的会让调用方用一份不完整的映射覆盖掉磁盘上的旧数据，
// 内部 ID 就此从 1 重排、旧权限与订阅静默迁到别的群（IDMap 收到错误会转成存储只读模式，
// 于是本次运行一个字都不会写，磁盘上的坏文件留着还能人工救）。
func (s *JSONStore) readLocked() (*stateFile, error) {
	raw, err := os.ReadFile(s.path)
	mainMissing := false
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: 读取状态文件 %s 失败：%w", ConfigKeyPrefix, s.path, err)
		}
		mainMissing = true
	} else if len(strings.TrimSpace(string(raw))) == 0 {
		// 主文件在但空的：内容上等同没有，同样先试备份（备份比它可信）
		mainMissing = true
	}

	if mainMissing {
		if bak, ok := s.readBackup(); ok {
			logger.Warnf("%s: 主状态文件 %s 不存在（被删除、换了工作目录，或写盘中断？），"+
				"已从备份 %s 恢复内部 ID 与群清单", ConfigKeyPrefix, s.path, s.backupPath())
			s.noteStoredAppID(bak.AppID)
			return bak, nil
		}
		s.warnFirstRun()
		return s.emptyState(), nil
	}

	st := s.emptyState()
	if err := json.Unmarshal(raw, st); err != nil {
		if bak, ok := s.readBackup(); ok {
			logger.Errorf("%s: 主状态文件 %s 不可用（解析失败：%v），已从备份 %s 恢复内部 ID 与群清单；"+
				"请检查磁盘与权限，下次写盘会把主文件修回来", ConfigKeyPrefix, s.path, err, s.backupPath())
			s.noteStoredAppID(bak.AppID)
			return bak, nil
		}
		logger.Errorf("%s: 主状态文件 %s 不可用（解析失败：%v）且没有可用备份：本次运行不会写盘，"+
			"内部 ID 无法恢复，请人工修复或确认后删除该文件", ConfigKeyPrefix, s.path, err)
		return nil, fmt.Errorf("%s: 状态文件 %s 解析失败：%w", ConfigKeyPrefix, s.path, err)
	}
	if st.IDMap == nil {
		st.IDMap = make(map[string]int64)
	}
	s.noteStoredAppID(st.AppID)
	return st, nil
}

// emptyState 是一份「什么都没有」的状态。
func (s *JSONStore) emptyState() *stateFile {
	return &stateFile{Version: stateFileVersion, IDMap: make(map[string]int64)}
}

// backupPath 返回备份文件路径。
func (s *JSONStore) backupPath() string {
	return s.path + stateFileBackupSuffix
}

// readBackup 读备份文件。不存在、为空或不是合法状态文件时返回 ok=false。
//
// 「备份存在但读不动」要单独记一条 Warn：不然调用方只会说「没有可用备份」，
// 排查时分不清是备份没生成还是备份也坏了。
func (s *JSONStore) readBackup() (*stateFile, bool) {
	raw, err := os.ReadFile(s.backupPath())
	if err != nil {
		return nil, false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, false
	}
	st := s.emptyState()
	if err := json.Unmarshal(raw, st); err != nil {
		logger.Warnf("%s: 备份文件 %s 不可用（%v）", ConfigKeyPrefix, s.backupPath(), err)
		return nil, false
	}
	if st.IDMap == nil {
		st.IDMap = make(map[string]int64)
	}
	return st, true
}

// warnFirstRun 在主文件与备份都不存在时喊一声。每次进程只喊一次。
//
// 这里必须吵：内部 ID 只存在这个文件里，lsp 的群权限与订阅又是按内部 ID 落盘的。
// 空状态不是错误（首次运行就该这样），但「不是首次却空」就是灾难的前兆。
func (s *JSONStore) warnFirstRun() {
	if s.firstRunWarned {
		return
	}
	s.firstRunWarned = true
	logger.Warnf("%s: 状态文件 %s 不存在（备份 %s 也没有）：按首次运行处理，"+
		"内部 ID 将从 1 重新分配。如果这不是第一次启动，说明状态文件已经丢了 —— "+
		"历史群权限与订阅会错位到别的群。不要删除该文件：内部 ID 只存在这里。",
		ConfigKeyPrefix, s.path, s.backupPath())
}

// noteStoredAppID 在读盘后比对「文件里记的 appID」与「当前配置的 appID」。
//
// openid 是 per-bot 的：换了 appID，旧 openid 全部失效，而文件里的旧群与旧映射
// 仍会被当成当前机器人的东西重新登记。上层只在启动时比对一次（adapter.go），
// 而且第一次写盘就把文件里的 app_id 改写成新值 —— 那条线索之后就查不到了，
// 所以这里在读盘路径上再钉一条，含新旧 appID 与后果说明。同一个值只喊一次。
func (s *JSONStore) noteStoredAppID(stored string) {
	if stored == "" || s.appID == "" || stored == s.appID || s.appIDWarned == stored {
		return
	}
	s.appIDWarned = stored
	logger.Warnf("%s: 状态文件 %s 记录的 appID（%s）与当前配置的 appID（%s）不一致："+
		"openid 是 per-bot 的，文件里的旧映射与旧群对新机器人全部失效，"+
		"而且下一次写盘会把 app_id 改写成 %s、这条线索看不到了。换机器人请先归档或删除该状态文件。",
		ConfigKeyPrefix, s.path, stored, s.appID, s.appID)
}

// ErrStateAppIDMismatch 表示状态文件里记的 app_id 与当前配置不一致。
//
// 上层据此区分「换机器人了」与「文件读不动」：前者必须拒绝启动，后者走既有的
// 只读降级路径（见 CheckAppID 的说明）。
var ErrStateAppIDMismatch = errors.New("qqofficial: 状态文件记录的 app_id 与当前配置不一致")

// MigrateLegacyStateFile 把旧的无 appId 状态文件一次性迁移成带 appId 的新文件。
//
// 返回值：(是否真的迁移了, 没能迁移的原因)。原因只用于打日志 —— 调用方不因为它失败
// 而停止启动，最多是本次运行从空状态开始。
//
// 步骤与依据：
//  1. 新文件已存在 → 什么都不做。迁移只做一次，升级过一遍之后新文件就在了，
//     再搬一次只会覆盖掉更新的数据。
//  2. 旧文件不存在 → 什么都不做（新装用户根本没有旧文件）。
//  3. 旧文件必须先是**合法状态文件**：能按状态结构解析，且 app_id 要么为空
//     （更老的版本没有这个字段、或手写/清洗过）要么等于当前配置。解析不了、
//     或者 app_id 是别的机器人的，一律不搬 —— 把读不懂 / 属于别人的数据搬进
//     当前机器人的文件，比不搬危险得多；这种情况把原因交给用户自己处理。
//  4. 写入用「临时文件 → fsync → chmod → rename」的原子替换（与 writeLocked 同一套
//     写法）：目标文件要么不存在、要么就是完整的一份旧状态，绝不会出现读到一半的文件。
//  5. 旧文件改名成 <旧名>.migrated 而不是删除：迁移判错时还能手动还原。
//
// 没一起搬的东西：旧文件旁边的 <旧名>.bak 不动。新文件第一次写盘会自己生成新备份，
// 「主文件 + 备份」这条退路仍然成立；升级瞬间只是少了一版可退，而旧文件本身还在
// （改名成 .migrated），人工恢复也够用。
func MigrateLegacyStateFile(legacyPath, newPath, appID string) (bool, error) {
	legacyPath = strings.TrimSpace(legacyPath)
	newPath = strings.TrimSpace(newPath)
	if legacyPath == "" || newPath == "" || legacyPath == newPath {
		return false, nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("检查目标状态文件 %s 失败：%w", newPath, err)
	}

	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("读取旧状态文件 %s 失败：%w", legacyPath, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false, fmt.Errorf("旧状态文件 %s 是空的，没有可继承的内容", legacyPath)
	}
	st := &stateFile{}
	if err := json.Unmarshal(raw, st); err != nil {
		return false, fmt.Errorf("旧状态文件 %s 不是合法状态文件（解析失败：%v）", legacyPath, err)
	}
	if stored := strings.TrimSpace(st.AppID); stored != "" && appID != "" && stored != appID {
		return false, fmt.Errorf("旧状态文件 %s 记录的是别的机器人（app_id=%s，当前配置=%s）",
			legacyPath, stored, appID)
	}

	dir := filepath.Dir(newPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, fmt.Errorf("创建状态文件目录 %s 失败：%w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".qqofficial-state-*.tmp")
	if err != nil {
		return false, fmt.Errorf("创建临时状态文件失败：%w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	// 原样搬字节：迁移只换文件名，内容一个字节都不动 —— 内部 ID 的一致性靠这个保证
	if _, err := tmp.Write(raw); err != nil {
		cleanup()
		return false, fmt.Errorf("写临时状态文件失败：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return false, fmt.Errorf("迁移落盘失败：%w", err)
	}
	if err := tmp.Chmod(stateFileMode); err != nil {
		cleanup()
		return false, fmt.Errorf("设置迁移后状态文件权限失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("关闭临时状态文件失败：%w", err)
	}
	if err := os.Rename(tmpName, newPath); err != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("把迁移结果放到 %s 失败：%w", newPath, err)
	}

	// 旧文件退场（改名保留）。这一步失败只影响「能不能手动还原」，迁移本身已经成功，
	// 所以只记一条 Warn：绝不能因为收拾旧文件失败就把已经搬好的新文件当成没搬。
	if err := os.Rename(legacyPath, legacyPath+stateFileMigratedSuffix); err != nil {
		logger.Warnf("%s: 状态文件已迁移到 %s，但旧文件 %s 改名失败（%v）：它还在原地，"+
			"下次启动不会再用它（新文件已存在），你可以手动删除或改名",
			ConfigKeyPrefix, newPath, legacyPath, err)
	}
	return true, nil
}

// writeLocked 原子写：临时文件 → fsync → rename。
func (s *JSONStore) writeLocked(st *stateFile) error {
	st.Version = stateFileVersion
	st.AppID = s.appID
	st.UpdatedAt = time.Now().Format(time.RFC3339)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: 状态序列化失败：%w", ConfigKeyPrefix, err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".qqofficial-state-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: 创建临时状态文件失败：%w", ConfigKeyPrefix, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(raw); err != nil {
		cleanup()
		return fmt.Errorf("%s: 写临时状态文件失败：%w", ConfigKeyPrefix, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%s: 状态文件落盘失败：%w", ConfigKeyPrefix, err)
	}
	if err := tmp.Chmod(stateFileMode); err != nil {
		cleanup()
		return fmt.Errorf("%s: 设置状态文件权限失败：%w", ConfigKeyPrefix, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: 关闭临时状态文件失败：%w", ConfigKeyPrefix, err)
	}
	// 覆盖主文件之前先把当前这一版留成备份：主文件丢了还能把内部 ID 认回来
	if err := s.rotateBackupLocked(); err != nil {
		// 备份只是兜底，不是前置条件：失败记一条日志就够，绝不能拖垮主写入路径
		logger.Warnf("%s: 状态文件备份轮转失败（%v），继续写入主文件，这一版备份没留下",
			ConfigKeyPrefix, err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: 替换状态文件失败：%w", ConfigKeyPrefix, err)
	}
	return nil
}

// rotateBackupLocked 把当前主文件轮转成同目录备份 <path>.bak。调用方必须持有 s.mu。
//
// 用 rename 而不是复制：备份要么是完整的旧文件、要么根本不存在，绝不会是半个 JSON；
// 而且 rename 是原子的，代价与文件大小无关。轮转失败的后果只是「没有上一版可退」，
// 所以错误由调用方记日志，不影响这次写入。
//
// 两种情况下跳过轮转：
//   - 主文件不存在或为空（首次写盘，没有上一版可留）；
//   - 主文件内容不是合法 JSON（它已经坏了）—— 此时若照常轮转，就会用坏内容盖掉
//     可能还有用的上一版备份，把「从备份恢复」这条退路自己拆掉。
func (s *JSONStore) rotateBackupLocked() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取待轮转的状态文件失败：%w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("主状态文件 %s 不是合法 JSON，跳过轮转以保留上一版备份", s.path)
	}
	if err := os.Rename(s.path, s.backupPath()); err != nil {
		return fmt.Errorf("把当前状态文件轮转成备份 %s 失败：%w", s.backupPath(), err)
	}
	return nil
}

// Load 实现 Persister：读 idmap 段。
func (s *JSONStore) Load() (map[string]int64, error) {
	if s == nil {
		return nil, fmt.Errorf("%s: 状态存储为空", ConfigKeyPrefix)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	return st.IDMap, nil
}

// Save 实现 Persister：只替换 idmap 段，群清单原样保留。
func (s *JSONStore) Save(m map[string]int64) error {
	if s == nil {
		return fmt.Errorf("%s: 状态存储为空", ConfigKeyPrefix)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return err
	}
	if m == nil {
		m = make(map[string]int64)
	}
	st.IDMap = m
	return s.writeLocked(st)
}

// LoadGroups 读群清单。
func (s *JSONStore) LoadGroups() ([]GroupRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("%s: 状态存储为空", ConfigKeyPrefix)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	return st.Groups, nil
}

// SaveGroups 只替换群清单段，idmap 原样保留。
func (s *JSONStore) SaveGroups(recs []GroupRecord) error {
	if s == nil {
		return fmt.Errorf("%s: 状态存储为空", ConfigKeyPrefix)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return err
	}
	if recs == nil {
		recs = make([]GroupRecord, 0)
	}
	st.Groups = recs
	return s.writeLocked(st)
}

// CheckAppID 比对「文件里记的 app_id」与「当前配置的 app_id」，不一致返回错误。
//
// 为什么启动时要**硬拒**，而不是像 noteStoredAppID 那样只 Warn 一句（暂时性决策，
// 2026-11 定）：writeLocked 每次写盘都会把 app_id 改写成当前值，而刷盘间隔只有 2 秒
// （见 adapter.go 的 stateFlushInterval）—— 那条 Warn 留下的线索活不过两秒。换机器人
// 之后继续用旧文件，等于让新机器人继承旧机器人的 openid 映射与群清单：旧群仍被当成
// 当前机器人的群，内部 ID 归属全错，而上层（lsp）是按内部 ID 判群权限与订阅的。
//
// 放行条件：
//   - app_id 为空：更老的版本没有这个字段，或者用户手写过/清洗过文件 —— 无从判断，放行；
//   - 与配置一致：正常情况。
//
// 文件读不动（损坏且没有可用备份）**不在这里**报错：那条路径已经有 IDMap 只读降级
// 与醒目 Error 日志，在这里再拒一次会把「还能人工救回来」的场景变成「程序起不来」。
//
// 何时该重新评估：腾讯若改成跨机器人稳定的 openid（那就没有「换了机器人旧映射失效」
// 这回事），这条硬拒就成了纯粹的麻烦，应当退回只告警。
func (s *JSONStore) CheckAppID() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return nil
	}
	stored := strings.TrimSpace(st.AppID)
	cur := strings.TrimSpace(s.appID)
	if stored == "" || cur == "" || stored == cur {
		return nil
	}
	return fmt.Errorf("%w：状态文件 %s 里记的 app_id 是 %s，当前配置 qqofficial.%s 是 %s。"+
		"openid 是 per-bot 的（同一个群在不同机器人下 openid 完全不同），这份映射与群清单对新机器人全部失效，"+
		"继续用下去会让旧群按旧内部 ID 继续持有权限与订阅。换机器人请先归档（改名）或删除该状态文件；"+
		"如果是 app-id 写错了，请把配置改回 %s。",
		ErrStateAppIDMismatch, s.path, stored, ConfigKeyAppID, cur, stored)
}

// StoredAppID 返回文件里记着的 appID（没写过则为空）。
//
// 用途只剩「查一下文件里记的是谁」这类诊断/日志：启动判决走 CheckAppID（不一致直接
// 拒绝启动）。换 AppID = 全部 openid 失效（实测同一群在个人/企业机器人下 openid 完全不同）。
func (s *JSONStore) StoredAppID() (string, error) {
	if s == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readLocked()
	if err != nil {
		return "", err
	}
	return st.AppID, nil
}
