package style

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTerminal 验证非终端、NO_COLOR、TERM=dumb 都不算可着色终端。
func TestTerminal(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer devNull.Close()

	assert.False(t, Terminal(nil))
	assert.False(t, Terminal(devNull))

	t.Setenv("NO_COLOR", "1")
	assert.False(t, Terminal(os.Stdout))

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	assert.False(t, Terminal(os.Stdout))
}

// TestPaint 验证着色开关与空颜色码的行为。
func TestPaint(t *testing.T) {
	assert.Equal(t, "x", Paint(false, Green, "x"))
	assert.Equal(t, "x", Paint(true, "", "x"))
	assert.Equal(t, "\x1b[32mx\x1b[0m", Paint(true, Green, "x"))
	assert.Equal(t, "\x1b[1;36mx\x1b[0m", Paint(true, Bold+";"+Cyan, "x"))
}
