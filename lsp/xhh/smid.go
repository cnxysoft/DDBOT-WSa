package xhh

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
)

const smidV2Expire = time.Hour * 24 * 365 // smidv2 有效期约 1 年

// GenerateSmidV2 生成 smidV2
// 结构：yyyyMMddHHmmss + md5(uuid) + "00" + md5("smsk_web_" + part1).Substring(0, 14) + "0"
// 其中 part1 = ts + md5(uuid) + "00"
func GenerateSmidV2() string {
	ts := formatNow()
	uid := getUid()
	part1 := ts + md5Hex(uid) + "00"
	part2 := md5Hex("smsk_web_" + part1)
	if len(part2) < 14 {
		part2 = part2 + strings.Repeat("0", 14-len(part2))
	}
	return part1 + part2[:14] + "0"
}

// formatNow 返回 14 位时间戳：yyyyMMddHHmmss
func formatNow() string {
	return time.Now().Format("20060102150405")
}

// getUid 返回 UUID，格式类似：xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
func getUid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:]))
}

// md5Hex 计算字符串的 MD5（UTF-8，小写 32 位 hex）
func md5Hex(input string) string {
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

// GetSmidV2 获取 smidv2，优先使用配置的 token，如果没有配置则尝试获取或生成持久化的 smidv2
func GetSmidV2() (string, error) {
	configToken := getConfigToken()
	if configToken != "" {
		return configToken, nil
	}

	// 尝试获取持久化的 smidv2
	smidv2, err := getStoredSmidV2()
	if err == nil && smidv2 != "" {
		return smidv2, nil
	}

	// 生成新的 smidv2 并存储
	newSmidV2 := GenerateSmidV2()
	err = storeSmidV2(newSmidV2)
	if err != nil {
		logger.Warnf("存储 smidv2 失败: %v", err)
	}
	return newSmidV2, nil
}

// getConfigToken 获取配置文件中的 token
func getConfigToken() string {
	return cfg.GetHeyboxToken()
}

// GetAndRefreshSmidV2 获取 smidv2，请求失败时生成新的替换
// 返回值：新的 smidv2，是否进行了替换
func GetAndRefreshSmidV2(currentSmidV2 string) (string, bool, error) {
	configToken := getConfigToken()
	if configToken != "" {
		return configToken, false, nil
	}

	// 记录旧 smidv2 使用天数
	oldDays := getSmidV2UsageDays()

	// 生成新的 smidv2
	newSmidV2 := GenerateSmidV2()
	err := storeSmidV2(newSmidV2)
	if err != nil {
		logger.Warnf("存储 smidv2 失败: %v", err)
		return newSmidV2, true, err
	}

	logger.Infof("smidv2 已替换（上一个使用了 %d 天）", oldDays)
	return newSmidV2, true, nil
}

// getStoredSmidV2 获取持久化的 smidv2
func getStoredSmidV2() (string, error) {
	return buntdb.Get(buntdb.XHHSmidV2Key(), buntdb.IgnoreNotFoundOpt())
}

// storeSmidV2 存储 smidv2 及其生成时间
func storeSmidV2(smidv2 string) error {
	err := buntdb.Set(buntdb.XHHSmidV2Key(), smidv2, buntdb.SetExpireOpt(smidV2Expire))
	if err != nil {
		return err
	}
	// 存储生成时间
	generateTime := time.Now().Unix()
	return buntdb.SetInt64(buntdb.XHHSmidV2GenerateTimeKey(), generateTime, buntdb.SetExpireOpt(smidV2Expire))
}

// getSmidV2UsageDays 获取当前持久化 smidv2 已使用的天数
func getSmidV2UsageDays() int {
	generateTime, err := buntdb.GetInt64(buntdb.XHHSmidV2GenerateTimeKey(), buntdb.IgnoreNotFoundOpt())
	if err != nil {
		return 0
	}
	elapsed := time.Since(time.Unix(generateTime, 0))
	return int(elapsed.Hours() / 24)
}
