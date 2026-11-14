package qqofficial

// 群信息接口冷却的粒度（P2）。
//
// 缺陷形态：fetchGroupInfo 用一个**全局** atomic 时间戳做闸门（原 adapter.go:962 的 infoBlock、
// 1948 写、1934 读）。任一集群权限错误（11253）就把所有群一起挡 30 分钟 ——
// 可是「这个群没权限」跟「那个群没权限」是两件事，新加的群因此拿不到群名，
// 而且谁先撞上谁就把后面所有群一起拖住。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	cooldownBadGroup  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cooldownGoodGroup = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// groupInfoPlatform 是只认 /v2/groups/{openid}/info 的假平台：
// cooldownBadGroup 回实测的权限错误（11253 + 40012010），其余群回正常群信息。
type groupInfoPlatform struct {
	*httptest.Server

	mu    sync.Mutex
	calls map[string]int
}

func newGroupInfoPlatform(t *testing.T) *groupInfoPlatform {
	t.Helper()
	p := &groupInfoPlatform{calls: make(map[string]int)}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, PathGetAppAccessToken) {
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
			return
		}
		if !strings.Contains(r.URL.Path, "/groups/") || !strings.HasSuffix(r.URL.Path, "/info") {
			_, _ = w.Write([]byte("{}"))
			return
		}
		openid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/groups/"), "/info")
		p.mu.Lock()
		p.calls[openid]++
		p.mu.Unlock()
		if openid == cooldownBadGroup {
			_, _ = w.Write([]byte(`{"message":"应用无接口访问权限","code":11253,"err_code":40012010}`))
			return
		}
		_, _ = w.Write([]byte(`{"group_openid":"` + openid + `","group_name":"好群","group_member_num":7}`))
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *groupInfoPlatform) callsFor(openid string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[openid]
}

func (p *groupInfoPlatform) dump() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, _ := json.Marshal(p.calls)
	return string(b)
}

// TestGroupInfoPermissionCooldownIsPerGroup：一个群撞上权限错误，不许把别的群一起挡住。
func TestGroupInfoPermissionCooldownIsPerGroup(t *testing.T) {
	p := newGroupInfoPlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	// 真实链路里 fetchGroupInfo 一定发生在 groups.ensure 之后（进群/成员事件先建群记录）。
	a.groups.ensure(cooldownBadGroup)
	a.groups.ensure(cooldownGoodGroup)
	a.fetchGroupInfo(cooldownBadGroup)  // 权限错误 -> 只有这个群进冷却
	a.fetchGroupInfo(cooldownGoodGroup) // 另一个群必须照常请求

	if n := p.callsFor(cooldownGoodGroup); n == 0 {
		t.Fatalf("群 %s 不该被另一个群的权限错误挡住（全局闸门就是这个毛病），"+
			"假平台收到的请求：%s", cooldownGoodGroup, p.dump())
	}
	if rec, ok := a.groups.getByOpenID(cooldownGoodGroup); !ok || rec.Name != "好群" {
		t.Errorf("另一个群的群名应正常缓存，实际 ok=%v rec=%+v", ok, rec)
	}

	// 冷却期内同一个群不再重复请求（保留原意：别对权限错误死磕）。
	a.fetchGroupInfo(cooldownBadGroup)
	if n := p.callsFor(cooldownBadGroup); n != 1 {
		t.Errorf("进了冷却的那个群在冷却期内不该再请求，实际请求了 %d 次", n)
	}
}
