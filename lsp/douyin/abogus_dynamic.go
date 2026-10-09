package douyin

import (
	"net/url"
	"strconv"
	"time"
)

// 动态管线常量（全部取自成品 linux-x64 二进制）。
const (
	// 浏览器信息串（成品 .rodata 0x10d790b，44 字节；长度写入槽 L79/L80）。
	douyinDynamicBrowserInfo = "2048|960|2554|1386|2560|1392|2560|1440|Win32"
	// 动态管线最终封装密钥（成品 d67905：movb $0xd3，单字节 RC4 密钥，与公开资料一致；
	// 静态管线用的是 0x79，见 abogus.go 的 douyinABogusStaticKey）。
	douyinDynamicPayloadKey = 0xD3
	// 14 天纪元/步长：fortnights = (UnixMilli(now) − 1721836800000) / 1209600000
	// （成品常数 0xffffc5ec085aa000 与除法 magic 0x719f9bf48f16b2e3，sar 29）。
	douyinDynamicFortnightEpochMS = int64(1721836800000)
	douyinDynamicFortnightMS      = int64(1209600000)
	// 槽位常量：L24=41(0x29)、L27=6(调用桶)、L35=1(环境标志)、L38=0x81(NR 字节，
	// 公开资料为 0x21，成品字节为准)、L44=14(检测标志)、L66=3(绊线值)。
	douyinDynamicSlot24          = 41
	douyinDynamicCallBucket      = 6
	douyinDynamicEnvFlags        = 1
	douyinDynamicNRFlags         = 0x81
	douyinDynamicDetectFlags     = 14
	douyinDynamicTripwire        = 3
	douyinDynamicPageID      int = 6241
	douyinDynamicAid         int = 6383
)

// 输出顺序表（成品 .rodata 0x114eb18，即公开资料的 FIELD_ORDER）与
// 校验和顺序表（0x114e988，为输出表的升序排列）。
var douyinDynamicFieldOrder = [50]int{34, 44, 56, 61, 73, 29, 70, 45, 35, 49, 38, 66, 51, 68,
	28, 48, 64, 47, 30, 71, 26, 55, 31, 69, 59, 40, 62, 63, 27, 72, 41, 74, 57, 52, 42, 39, 33,
	67, 53, 43, 65, 46, 36, 24, 60, 32, 79, 80, 84, 85}

var douyinDynamicChecksumOrder = [50]int{24, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 38, 39,
	40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 51, 52, 53, 55, 56, 57, 59, 60, 61, 62, 63, 64, 65,
	66, 67, 68, 69, 70, 71, 72, 73, 74, 79, 80, 84, 85}

// spread 的噪声/数据掩码（成品 0xd67ba0 栈上字 0x6e91 与长字 0xd32cbd42
// → 字节序列 [0x91,0x6E,0x42,0xBD,0x2C,0xD3]）。
var (
	douyinDynamicNoiseMasks = [3]byte{0x91, 0x42, 0x2C}
	douyinDynamicDataMasks  = [3]byte{0x6E, 0xBD, 0xD3}
)

// appendDouyinDynamicABogus 为作品详情等请求追加动态版 a_bogus 参数。
// 成品流程（0xd66ea0）：解析 URL 取 RawQuery → 清理 UA → generateDouyinDynamicABogus
// → 拼 &a_bogus=<sig>（concatstring3，无静态管线那个尾部 '='）。
func appendDouyinDynamicABogus(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	ua := douyinSignatureUserAgent(activeDouyinUserAgent())
	signature := generateDouyinDynamicABogus(u.RawQuery, ua)
	return rawURL + douyinParamABogusPrefix + signature
}

// generateDouyinDynamicABogus 计算动态管线 a_bogus 签名值（成品 0xd66fa0，逐指令复原）。
//
// 86 个按 L 号寻址的字节槽 → 版本块 mix(1,0,0)+mix(1,0,2) →
// 校验和 = XOR(版本块字节) ⊕ XOR(fields[升序表]) →
// 体 = fields[输出表×50] + 浏览器信息串 + tail + 校验和低字节 →
// frame = spread(体) → 封装 = 动态 RC4({0xD3}, 版本块+frame) →
// 结果 = 无填充 base64(header=mix(3,82,1) 前置 + 封装, s4 alphabet)。
func generateDouyinDynamicABogus(query string, ua string) string {
	queryDigest := sm3Digest(sm3Digest([]byte(query + douyinABogusSalt)))
	saltDigest := sm3Digest(sm3Digest([]byte(douyinABogusSalt)))
	uaSealed := douyinDynamicBase64(douyinDynamicRC4([]byte(douyinABogusUAKey), []byte(ua)), douyinABogusAlphabetS3)
	uaDigest := sm3Digest([]byte(uaSealed))

	now := time.Now().UnixMilli()
	ink := now - 1
	fortnights := (now - douyinDynamicFortnightEpochMS) / douyinDynamicFortnightMS
	tailValue := byte(now + 3)
	tail := strconv.FormatInt(int64(tailValue), 10) + ","

	// 86 个字节槽（栈上 0xe4 数组，索引即 L 号）。
	var fields [86]byte
	fields[24] = douyinDynamicSlot24
	fields[26] = byte(fortnights)
	fields[27] = douyinDynamicCallBucket
	fields[28] = tailValue
	fields[29] = byte(now)
	fields[30] = byte(now >> 8)
	fields[31] = byte(now >> 16)
	fields[32] = byte(now >> 24)
	fields[33] = byte(now >> 32)
	fields[34] = byte(now >> 40)
	fields[35] = douyinDynamicEnvFlags
	fields[38] = douyinDynamicNRFlags
	fields[44] = douyinDynamicDetectFlags
	// 摘要字节槽：query 链 (9,18) 绊线 3、盐链 (10,19) 绊线 4、UA 链 (11,21) 绊线 5；
	// 成品不写 canary 槽（L50/L54/L58 恒 0）。
	fields[48] = queryDigest[9]
	fields[49] = queryDigest[18]
	fields[51] = queryDigest[3]
	fields[52] = saltDigest[10]
	fields[53] = saltDigest[19]
	fields[55] = saltDigest[4]
	fields[56] = uaDigest[11]
	fields[57] = uaDigest[21]
	fields[59] = uaDigest[5]
	for i := 0; i < 6; i++ {
		fields[60+i] = byte(ink >> (8 * i))
	}
	fields[66] = douyinDynamicTripwire
	for i := 0; i < 4; i++ {
		fields[67+i] = byte(douyinDynamicPageID >> (8 * i))
		fields[71+i] = byte(douyinDynamicAid >> (8 * i))
	}
	fields[79] = byte(len(douyinDynamicBrowserInfo))
	fields[80] = byte(len(douyinDynamicBrowserInfo) >> 8)
	fields[84] = byte(len(tail))
	fields[85] = byte(len(tail) >> 8)

	version := douyinDynamicMix(1, 0, 0)
	version = append(version, douyinDynamicMix(1, 0, 2)...)
	checksum := 0
	for _, b := range version {
		checksum ^= int(b)
	}
	for _, idx := range douyinDynamicChecksumOrder {
		checksum ^= int(fields[idx])
	}
	body := make([]byte, 0, len(douyinDynamicFieldOrder)+len(douyinDynamicBrowserInfo)+len(tail)+1)
	for _, idx := range douyinDynamicFieldOrder {
		body = append(body, fields[idx])
	}
	body = append(body, douyinDynamicBrowserInfo...)
	body = append(body, tail...)
	body = append(body, byte(checksum))

	frame := douyinDynamicSpread(body)
	sealed := douyinDynamicRC4([]byte{douyinDynamicPayloadKey}, append(version, frame...))
	header := douyinDynamicMix(3, 82, 1)
	return douyinDynamicBase64(append(header, sealed...), douyinABogusAlphabetS4)
}

// douyinDynamicMix 生成 4 字节掩码块（成品 0xd67a60）：
// noise=secureDouyinInt(0xFFFF) 拆两个字节；
// mode==1 时高位字节清零（header 用）；mode==2 时低位字节换 probe 噪声
// （secureDouyinInt(0xF0)，>109 时强制奇数 +1）、高位字节固定 0xB2（版本块第二段用）；
// 输出与 (a,b) 两字节做 0xAA/0x55 交叉或。
func douyinDynamicMix(a int, b int, mode int) []byte {
	n := secureDouyinInt(0xFFFF)
	low := n & 0xFF
	high := (n >> 8) & 0xFF
	if mode == 1 {
		high = 0
	}
	if mode == 2 {
		p := secureDouyinInt(0xF0)
		if p > 109 {
			p = p + p&1 + 1
		}
		low = p
		high = 0xB2
	}
	return []byte{
		byte((a & 0x55) | (low & 0xAA)),
		byte((a & 0xAA) | (low & 0x55)),
		byte((b & 0x55) | (high & 0xAA)),
		byte((b & 0xAA) | (high & 0x55)),
	}
}

// douyinDynamicSpread 按 3 字节组展开噪声（成品 0xd67ba0）：
// 每组抽一次 secureDouyinInt(1000)，输出 4 字节：
//
//	out0 = (d0&DATA0) | (n&NOISE0)
//	out1 = (d1&DATA1) | (n&NOISE1)
//	out2 = (d2&DATA2) | (n&0xFF)   —— 成品此处用完整噪声字节，不做 NOISE2 掩码
//	out3 = (d0&NOISE0) | (d1&NOISE1) | (d2&NOISE2)
//
// 末尾不足 3 字节的原样追加。
func douyinDynamicSpread(data []byte) []byte {
	out := make([]byte, 0, len(data)+4)
	i := 0
	for ; i+2 < len(data); i += 3 {
		n := byte(secureDouyinInt(1000))
		out = append(out,
			(data[i]&douyinDynamicDataMasks[0])|(n&douyinDynamicNoiseMasks[0]),
			(data[i+1]&douyinDynamicDataMasks[1])|(n&douyinDynamicNoiseMasks[1]),
			(data[i+2]&douyinDynamicDataMasks[2])|n,
			(data[i]&douyinDynamicNoiseMasks[0])|(data[i+1]&douyinDynamicNoiseMasks[1])|(data[i+2]&douyinDynamicNoiseMasks[2]),
		)
	}
	return append(out, data[i:]...)
}

// douyinDynamicRC4 动态版 RC4（成品 0xd67ea0，与标准 RC4 有三处差异）：
// S 盒降序初始化 box[i]=255-i；
// KSA 为 j = (j*(box[i]+1) + key[i%len(key)]) % 256（imul 乘法调度）；
// PRGA 的 i 槽整体偏移 +1（访问 box[i+1]，box[0] 永不作为 i 槽，成品 0x19(%rsp) 基址）。
func douyinDynamicRC4(key []byte, data []byte) []byte {
	var box [256]byte
	for i := 0; i < 256; i++ {
		box[i] = byte(255 - i)
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j*(int(box[i])+1) + int(key[i%len(key)])) % 256
		box[i], box[j] = box[j], box[i]
	}
	out := make([]byte, len(data))
	i := 0
	j = 0
	for k := 0; k < len(data); k++ {
		i = (i + 1) % 256
		j = (j + int(box[i+1])) % 256
		box[i+1], box[j] = box[j], box[i+1]
		out[k] = data[k] ^ box[(int(box[i+1])+int(box[j]))%256]
	}
	return out
}

// douyinDynamicBase64 动态版结果编码：alphabet 直接传参的无填充自定义 base64
// （成品 0xd68100，编码主体与 douyinBase64Encode 相同）。
func douyinDynamicBase64(data []byte, alphabet string) string {
	return douyinBase64Encode(data, alphabet)
}
