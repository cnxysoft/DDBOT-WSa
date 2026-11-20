package admin

import "testing"

func TestPanelURL(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"127.0.0.1:15631", "http://127.0.0.1:15631/"},
		{"0.0.0.0:15631", "http://127.0.0.1:15631/"},
		{"[::]:15631", "http://127.0.0.1:15631/"},
		{"192.168.1.5:8080", "http://192.168.1.5:8080/"},
		{"localhost:15631", "http://localhost:15631/"},
		{"没有端口", "http://没有端口/"},
	}
	for _, c := range cases {
		if got := panelURL(c.addr); got != c.want {
			t.Errorf("panelURL(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestSetupNeededDefaultToken(t *testing.T) {
	s := &Server{token: defaultAdminToken, tokenDefault: true}
	if !s.setupNeeded() {
		t.Fatal("默认密码 520 首次启动时应保持初始化向导可用")
	}
	s.setTokenDefault(false)
	if s.setupNeeded() {
		t.Fatal("初始化完成后 setup 接口必须关闭")
	}
	if !(&Server{}).setupNeeded() {
		t.Fatal("测试里直接构造的空 token Server 应视为首次启动")
	}
	if (&Server{token: "configured"}).setupNeeded() {
		t.Fatal("已配置 token 时不应再进入初始化向导")
	}
}

func TestAdminTokenHint(t *testing.T) {
	cases := []struct {
		addr         string
		token        string
		tokenDefault bool
		want         string
	}{
		{"127.0.0.1:15631", "pw", false, "登录密码为配置里的 admin.token"},
		{"0.0.0.0:15631", "pw", false, "登录密码为配置里的 admin.token"},
		{"127.0.0.1:15631", defaultAdminToken, true, "admin.token 未配置，默认登录密码为 520；本机首次打开会进入初始化向导"},
		{"127.0.0.1:15631", "", false, "admin.token 未配置，首次打开会进入初始化向导"},
		{"0.0.0.0:15631", "", false, "admin.token 未配置且监听非回环地址，数据接口会返回 401，请配置 token 后重启"},
		{"[::]:15631", "", false, "admin.token 未配置且监听非回环地址，数据接口会返回 401，请配置 token 后重启"},
	}
	for _, c := range cases {
		if got := adminTokenHint(c.addr, c.token, c.tokenDefault); got != c.want {
			t.Errorf("adminTokenHint(%q, %q, %v) = %q, want %q", c.addr, c.token, c.tokenDefault, got, c.want)
		}
	}
}
