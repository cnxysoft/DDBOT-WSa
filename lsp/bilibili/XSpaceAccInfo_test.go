package bilibili

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXSpaceAccInfo(t *testing.T) {
	const RiskControlVerificationFailed = -352
	resp, err := XSpaceAccInfo(97505)
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	//assert.Equal(t, int(resp.GetCode()), RiskControlVerificationFailed)
}
