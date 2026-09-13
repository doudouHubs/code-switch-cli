//go:build windows

package services

import (
	"testing"
	"time"
)

func TestPetWindowPlatformMonitorCoalescesPendingEvents(t *testing.T) {
	type observedEvent struct {
		revision       uint64
		movingWindowID string
	}
	events := make(chan observedEvent, 2)
	monitor := &petWindowPlatformMonitor{
		started:         true,
		available:       true,
		eventGeneration: 1,
		emitEvent: func(revision uint64, movingWindowID string) {
			events <- observedEvent{revision: revision, movingWindowID: movingWindowID}
		},
	}

	monitor.queuePlatformEvent(1, "0x1")
	monitor.queuePlatformEvent(2, "0x2")

	select {
	case got := <-events:
		if got.revision != 2 || got.movingWindowID != "0x2" {
			t.Fatalf("coalesced platform event = %#v, want latest revision/window", got)
		}
	case <-time.After(petWindowPlatformEventInterval * 3):
		t.Fatal("timed out waiting for coalesced platform event")
	}

	// The timer is one-shot; after the latest event is delivered, the burst must
	// not create another callback. The wait is tied to the production throttle.
	select {
	case extra := <-events:
		t.Fatalf("unexpected duplicate platform event: %#v", extra)
	case <-time.After(petWindowPlatformEventInterval):
	}
}

func TestPetWindowPlatformMonitorStopDropsPendingEvent(t *testing.T) {
	events := make(chan struct{}, 1)
	monitor := &petWindowPlatformMonitor{
		started:         true,
		available:       true,
		eventGeneration: 1,
		emitEvent: func(uint64, string) {
			events <- struct{}{}
		},
	}

	monitor.queuePlatformEvent(1, "0x1")
	monitor.Stop()

	select {
	case <-events:
		t.Fatal("platform event was emitted after monitor stop")
	case <-time.After(petWindowPlatformEventInterval * 2):
	}
}
