package bilibili

import (
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"time"
)

const (
	// PathGetAttentionList 旧的 /feed/v1/feed/get_attention_list 已被 B 站下线（HTTP 404），
	// 改用 relation/followings/simple：它把当前账号的全部关注以 UID 数组返回，
	// data.list 的结构与旧接口完全一致，因此响应结构体可以继续复用。
	//
	// 待验证：本接口未带分页参数（pn/ps），这里假设「一次返回全部关注」。
	// 该假设仅在小关注数账号上实测过；官方的 relation/followings（非 simple）接口存在
	// 分页上限，若 simple 同样有上限，关注列表会被静默截断。
	// 因此调用方在使用列表前应通过 verifyAttentionListComplete 做一次运行时的条数交叉校验。
	PathGetAttentionList = "/x/relation/followings/simple"
)

func GetAttentionList() (*GetAttentionListResponse, error) {
	if !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	url := BPath(PathGetAttentionList)
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		AddUAOption(),
		requests.TimeoutOption(time.Second*10),
		delete412ProxyOption,
	)
	opts = append(opts, GetVerifyOption()...)
	getAttentionListResp := new(GetAttentionListResponse)
	// simple 接口是否理会分页参数尚未确认，故此处不带 pn/ps；
	// 返回条数的完整性由调用方的 verifyAttentionListComplete 做运行时交叉校验
	err := requests.Get(url, map[string]interface{}{
		"vmid": accountUid.String(),
	}, getAttentionListResp, opts...)
	if err != nil {
		return nil, err
	}
	return getAttentionListResp, nil
}

// verifyAttentionListComplete 用 /x/relation/stat 的 following 总数，
// 交叉校验 /x/relation/followings/simple 返回的关注列表是否被截断。
//
// 为什么需要：simple 接口的「一次返回全部关注」是未经确认的假设（见 PathGetAttentionList 注释）。
// 一旦接口存在条数上限，列表会比实际关注数少，调用方就会把「实际已关注但没出现在列表里」
// 的订阅目标误判为未关注，进而触发不必要的自动关注。
// 这里把不可静态验证的假设变成运行时可观测的兜底：条数不足时返回 false 并告警。
//
// 校验本身失败（网络错误、非 0 返回码）时返回 true，不阻塞原有流程，仅放弃本次校验。
func verifyAttentionListComplete(returnedCount int) bool {
	stat, err := XRelationStat(accountUid.Load())
	if err != nil {
		logger.WithField("err", err).Debug("verifyAttentionListComplete: XRelationStat 调用失败，跳过本次校验")
		return true
	}
	if stat.GetCode() != 0 {
		logger.WithField("code", stat.GetCode()).
			Debug("verifyAttentionListComplete: XRelationStat 返回非 0，跳过本次校验")
		return true
	}
	following := stat.GetData().GetFollowing()
	if following > int64(returnedCount) {
		logger.WithField("attention_list_len", returnedCount).
			WithField("following", following).
			Errorf("关注列表疑似被截断（返回 %d 条，实际关注 %d 个），"+
				"已跳过本次基于关注列表的自动同步；请确认 %s 是否需要分页参数",
				returnedCount, following, PathGetAttentionList)
		return false
	}
	return true
}
