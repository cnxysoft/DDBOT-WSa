package acfun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLivePage(t *testing.T) {
	useHTTPFixture(t, LiveUrl(19372766), livePageFixture)
	result, err := LivePage(19372766)
	require.NoError(t, err)
	assert.Equal(t, "admin", result.GetLiveInfo().GetUser().GetName())
	assert.Equal(t, "https://example.invalid/avatar.jpg", result.GetLiveInfo().GetUser().GetHeadUrl())
}
