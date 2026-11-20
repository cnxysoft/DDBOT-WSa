package telegram

import (
	"testing"
)

func TestParseInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"positive number", "123456", 123456},
		{"negative number", "-1002003004005", -1002003004005},
		{"zero", "0", 0},
		{"empty string", "", 0},
		{"invalid characters", "abc", 0},
		{"mixed valid and invalid", "123abc", 0},
		{"large positive", "9223372036854775807", 9223372036854775807},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseInt64(tt.input)
			if result != tt.expected {
				t.Errorf("parseInt64(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestErr(t *testing.T) {
	t.Run("Error method should return string", func(t *testing.T) {
		err := Err("test error message")
		if err.Error() != "test error message" {
			t.Errorf("Expected 'test error message', got '%s'", err.Error())
		}
	})

	t.Run("Empty error", func(t *testing.T) {
		err := Err("")
		if err.Error() != "" {
			t.Errorf("Expected empty string, got '%s'", err.Error())
		}
	})
}

func TestPublishNil(t *testing.T) {
	t.Run("Publish with nil MSG should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Publish panicked with nil MSG: %v", r)
			}
		}()
		Publish(nil)
	})
}

func TestSendToChatNil(t *testing.T) {
	t.Run("SendToChat with nil MSG should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("SendToChat panicked with nil MSG: %v", r)
			}
		}()
		SendToChat(123456, nil)
	})
}

func TestStartReceivingNil(t *testing.T) {
	t.Run("StartReceiving with nil callback should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("StartReceiving panicked with nil callback: %v", r)
			}
		}()
		StartReceiving(nil)
	})
}

func TestBuildTelegramHTTPClient(t *testing.T) {
	t.Run("buildTelegramHTTPClient should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("buildTelegramHTTPClient panicked: %v", r)
			}
		}()
		client := buildTelegramHTTPClient()
		// When no proxy is configured, it should return a client with default transport
		if client == nil {
			t.Log("HTTP client is nil (expected when no proxy configured)")
		}
	})
}
