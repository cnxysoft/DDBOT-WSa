package warn

import (
	"testing"
)

func TestWarn(t *testing.T) {
	// 测试 Warn 函数是否能正常执行而不崩溃
	t.Run("Warn should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warn panicked: %v", r)
			}
		}()

		Warn("test warning message")
	})

	t.Run("Warn with empty string", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warn panicked with empty string: %v", r)
			}
		}()

		Warn("")
	})

	t.Run("Warn with long message", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warn panicked with long message: %v", r)
			}
		}()

		longMsg := "This is a very long warning message that tests the Warn function's ability to handle lengthy content without any issues or panics."
		Warn(longMsg)
	})
}
