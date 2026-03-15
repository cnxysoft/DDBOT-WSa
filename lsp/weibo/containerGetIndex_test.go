package weibo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWeiboGuestCardsFlatten(t *testing.T) {
	primary := &Card{Id: 101, Mblogid: "primary"}
	nested := &Card{Id: 202, Mblogid: "nested"}

	resp := &apiContainerGetIndexGuestCardsResponse{
		Ok: 1,
		Data: &apiContainerGetIndexGuestCardsResponseData{
			Cards: []apiContainerGetIndexGuestCard{
				{Mblog: primary},
				{CardGroup: []apiContainerGetIndexGuestCard{{Mblog: nested}}},
			},
		},
	}

	cardsResp := resp.ToCardsResponse()
	assert.EqualValues(t, int32(1), cardsResp.GetOk())
	assert.NotNil(t, cardsResp.GetData())
	assert.Len(t, cardsResp.GetData().GetList(), 2)
	assert.Equal(t, primary, cardsResp.GetData().GetList()[0])
	assert.Equal(t, nested, cardsResp.GetData().GetList()[1])
}
