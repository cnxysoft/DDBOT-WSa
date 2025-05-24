package bilibili

import (
	"fmt"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/utils"
)

func TestSignWbi(t *testing.T) {

	params, _ := utils.ToDatas(&XSpaceAccInfoRequest{
		Mid:           28278764,
		Token:         "",
		Platform:      "web",
		WebLocation:   "1550101",
		DmImgList:     "[]",
		DmImgStr:      "V2ViR0wgMS4wIChPcGVuR0wgRVMgMi4wIENocm9taXVtKQ",
		DmCoverImgStr: "QU5HTEUgKE5WSURJQSwgTlZJRElBIEdlRm9yY2UgUlRYIDMwOTAgKDB4MDAwMDIyMDQpIERpcmVjdDNEMTEgdnNfNV8wIHBzXzVfMCwgRDNEMTEpR29vZ2xlIEluYy4gKE5WSURJQS",
		DmImgInter:    `{"ds":[],"wh":[4541,3287,113],"of":[20,40,20]}`,
	})

	// 调用函数进行签名
	signedParams := signWbi(params)

	// 输出签名的结果
	fmt.Println("签名结果:", signedParams)
}
