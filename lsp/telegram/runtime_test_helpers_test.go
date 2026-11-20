package telegram

import "testing"

func resetTelegramRuntimeForTest(t *testing.T) {
	t.Helper()
	managerMu.Lock()
	if current != nil {
		managerMu.Unlock()
		t.Fatal("test requires no active Telegram generation")
	}
	oldInitialized, oldReceiver := initialized, receiver
	oldChats, oldOffsets := globalChats, offsets
	initialized, receiver, globalChats = false, nil, nil
	offsets = make(map[string]int)
	managerMu.Unlock()
	t.Cleanup(func() {
		Close()
		managerMu.Lock()
		initialized, receiver = oldInitialized, oldReceiver
		globalChats, offsets = oldChats, oldOffsets
		managerMu.Unlock()
	})
}
