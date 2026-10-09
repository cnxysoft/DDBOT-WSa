package douyin

import (
	"encoding/json"
	"regexp"
	"sync"
	"time"
)

// douyinPostExtraRetention 是图集扩展元数据的内存保留时长（24 小时，自动清理）。
const douyinPostExtraRetention = 24 * time.Hour

// douyinPostExtra 是单个作品的图集扩展元数据（proto 字段面之外的原始 JSON 信息）。
type douyinPostExtra struct {
	Images       []string
	GalleryItems []DouyinGalleryItem
	StoredAt     time.Time
}

var (
	// douyinPostExtras 是成品 post_extra.go 的全局存储：internal/sync.HashTrieMap
	// （即 sync.Map 的底层实现）。成品访问路径：
	//   - rememberDouyinPostExtras @+0x395 → HashTrieMap.Swap（sync.Map.Store 内联）；
	//   - douyinPostGalleryItems / douyinPostImages @+0x4f → HashTrieMap.Load；
	//   - cleanupDouyinPostExtras @+0x41 → HashTrieMap.Range；
	//     其闭包 @+0x155 → HashTrieMap.LoadAndDelete（sync.Map.Delete 内联）。
	douyinPostExtras       sync.Map
	douyinImageSizePattern = regexp.MustCompile(`~(?:c5_)?\d+x\d+`)
	douyinTplSizePattern   = regexp.MustCompile(`~tplv-[A-Za-z0-9_-]+:\d+x\d+`)
)

// rememberDouyinPostExtras 解析作品列表原始响应，记录每条作品的图片与图集条目，
// 供模板数据（.pictures/.gallery/.live_photos）在推送时读取；内存保留 24 小时。
//
// 成品 @post_extra.go:18（992B）签名 func(raw json.RawMessage)：入口只保存一个切片实参
// （rax/rbx/rcx → 0x168/0x170/0x178），内部 json.Unmarshal ×2（@+0x8f/@+0x12a）后按
// aweme_list 逐条取 aweme_id（mapaccess1_faststr @+0x156）并调
// collectDouyinGalleryItems（@+0x1a1）一次性建图集，Images 由条目 ImageURL 派生，
// 最终写入全局 sync.Map（Store → HashTrieMap.Swap @+0x395）。唯一调用点 GetPosts @getUserPost.go:15
// +0x3a2 直接传 raw；三参旧形态 (awemeID,images,gallery) 不存在于成品，重建的
// rememberDouyinPostExtrasFromRaw 已按此合并删除。
func rememberDouyinPostExtras(raw json.RawMessage) {
	// 成品入口先 time.Now（@+0x35）再 cleanupDouyinPostExtras（@+0x52），
	// 无 len(raw)==0 早退；下面保持一致。
	now := time.Now()
	cleanupDouyinPostExtras(now)
	var parsed struct {
		AwemeList []json.RawMessage `json:"aweme_list"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return
	}
	for _, item := range parsed.AwemeList {
		// 成品第二次 Unmarshal 目标是 map[string]interface{}（type 0xf65ce0），
		// 随后 mapaccess1_faststr 取 "aweme_id"（rodata 0x10a4af6,len8）并断言 string。
		var card map[string]interface{}
		if err := json.Unmarshal(item, &card); err != nil {
			continue
		}
		awemeID, _ := card["aweme_id"].(string)
		if awemeID == "" {
			continue
		}
		// 成品改用 collectDouyinGalleryItems(card)（call site +0x1a1，0xd8d0e0）
		// 一次性建图集；Images 由图集的 ImageURL（结构偏移 +0x18）逐项过滤非空得到
		// （+0xd94d32 test len(ImageURL)），不再单独解析 images 数组。
		gallery := collectDouyinGalleryItems(card)
		if len(gallery) == 0 {
			continue
		}
		images := make([]string, 0, len(gallery))
		for _, g := range gallery {
			if g.ImageURL != "" {
				images = append(images, g.ImageURL)
			}
		}
		douyinPostExtras.Store(awemeID, &douyinPostExtra{
			Images:       images,
			GalleryItems: gallery,
			StoredAt:     now,
		})
	}
}

// cleanupDouyinPostExtras 清理超过保留时长的条目。
//
// 成品 @post_extra.go:49（128B）签名 func(now time.Time)：唯一 callee 是
// HashTrieMap.Range（@+0x41）；闭包 cleanupDouyinPostExtras.func1 只调
// time.Time.Sub（@+0x118，now.Sub(extra.StoredAt)）与 LoadAndDelete
// （@+0x155，sync.Map.Delete 内联）；时限 24h（立即数 0x4e94914f0000 = 86.4e12 ns）。
func cleanupDouyinPostExtras(now time.Time) {
	douyinPostExtras.Range(func(key, value interface{}) bool {
		extra, _ := value.(*douyinPostExtra)
		if extra == nil || now.Sub(extra.StoredAt) > douyinPostExtraRetention {
			douyinPostExtras.Delete(key)
		}
		return true
	})
}

// douyinPostGalleryItems 返回作品的图集条目。
func douyinPostGalleryItems(awemeID string) []DouyinGalleryItem {
	value, ok := douyinPostExtras.Load(awemeID)
	if !ok {
		return nil
	}
	extra, _ := value.(*douyinPostExtra)
	if extra == nil {
		return nil
	}
	return append([]DouyinGalleryItem(nil), extra.GalleryItems...)
}

// douyinPostImages 返回作品的图片地址列表（已规范化）。
func douyinPostImages(awemeID string) []string {
	value, ok := douyinPostExtras.Load(awemeID)
	if !ok {
		return nil
	}
	extra, _ := value.(*douyinPostExtra)
	if extra == nil {
		return nil
	}
	return append([]string(nil), extra.Images...)
}

// firstDouyinImageURL 在任意 JSON 结构里递归找第一张图片地址（成品 0xd952c0）：
// map 按 url_list/download_url_list/download_url/display_image/display_url/origin_cover/url/uri
// 的固定次序取值递归；数组逐项递归；字符串经 normalizeDouyinImageURL 规范化后返回。
func firstDouyinImageURL(v interface{}) string {
	switch node := v.(type) {
	case map[string]interface{}:
		for _, key := range []string{
			"url_list", "download_url_list", "download_url", "display_image",
			"display_url", "origin_cover", "url", "uri",
		} {
			if s := firstDouyinImageURL(node[key]); s != "" {
				return s
			}
		}
	case []interface{}:
		for _, item := range node {
			if s := firstDouyinImageURL(item); s != "" {
				return s
			}
		}
	case string:
		return normalizeDouyinImageURL(node)
	}
	return ""
}

// normalizeDouyinImageURL 规范化图片地址：
// 去掉缩略尺寸后缀，尽量取原图；保留查询串（含签名）。
func normalizeDouyinImageURL(imageURL string) string {
	if imageURL == "" {
		return ""
	}
	normalized := douyinTplSizePattern.ReplaceAllString(imageURL, "")
	normalized = douyinImageSizePattern.ReplaceAllString(normalized, "")
	return normalized
}
