package utils

import (
	"reflect"
	"testing"
)

func TestBeforeExitHooksDrainResourcesBeforeStorageCloses(t *testing.T) {
	previous := exitManager
	defer func() { exitManager = previous }()
	exitManager = &exitHookManager{}
	var events []string
	AddExitHook(func() { events = append(events, "primary-db") })
	AddExitHook(func() { events = append(events, "other-storage") })
	AddBeforeExitHook(func() { events = append(events, "resources") })
	AddBeforeExitHook(func() { events = append(events, "other-workers") })
	RunExitHooks()
	RunExitHooks()
	if !reflect.DeepEqual(events, []string{"resources", "other-workers", "primary-db", "other-storage"}) {
		t.Fatal("resources did not drain once before backing storage", events)
	}
}
