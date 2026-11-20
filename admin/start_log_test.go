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

func TestAdminTokenHint(t *testing.T) {
	cases := []struct{ addr, token, want string }{
		{"127.0.0.1:15631", "pw", "登录密码为配置里的 admin.token"},
		{"0.0.0.0:15631", "pw", "登录密码为配置里的 admin.token"},
		{"127.0.0.1:15631", "", "admin.token 未配置，首次打开会进入初始化向导"},
		{"0.0.0.0:15631", "", "admin.token 未配置且监听非回环地址，数据接口会返回 401，请配置 token 后重启"},
		{"[::]:15631", "", "admin.token 未配置且监听非回环地址，数据接口会返回 401，请配置 token 后重启"},
	}
	for _, c := range cases {
		if got := adminTokenHint(c.addr, c.token); got != c.want {
			t.Errorf("adminTokenHint(%q, %q) = %q, want %q", c.addr, c.token, got, c.want)
		}
	}
}
