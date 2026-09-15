package services

import (
	"reflect"
	"testing"
	"time"
)

func TestPetTypingControllerEmitsOnlyActivityEdges(t *testing.T) {
	now := time.UnixMilli(1_000)
	events := make([]PetTypingState, 0, 2)
	controller := NewPetTypingController(PetTypingControllerOptions{
		Now:  func() time.Time { return now },
		Emit: func(state PetTypingState) { events = append(events, state) },
	})
	defer controller.Close()

	controller.NotifyKey()
	controller.NotifyKey()

	if len(events) != 1 || !events[0].Active {
		t.Fatalf("active events = %#v, want one active edge", events)
	}
	if got := controller.State(); got.LastKeyAt != now.UnixMilli() || got.ExpiresAt != now.Add(PetTypingTimeout).UnixMilli() {
		t.Fatalf("active state = %#v", got)
	}

	now = now.Add(PetTypingTimeout)
	controller.mu.Lock()
	generation := controller.generation
	controller.mu.Unlock()
	controller.expire(generation)

	if len(events) != 2 || events[1].Active {
		t.Fatalf("edge events after timeout = %#v, want active then inactive", events)
	}
}

func TestPetTypingControllerStaleTimeoutCannotClearNewKey(t *testing.T) {
	now := time.UnixMilli(10_000)
	events := make([]PetTypingState, 0, 2)
	controller := NewPetTypingController(PetTypingControllerOptions{
		Now:  func() time.Time { return now },
		Emit: func(state PetTypingState) { events = append(events, state) },
	})
	defer controller.Close()

	controller.NotifyKey()
	controller.mu.Lock()
	oldGeneration := controller.generation
	controller.mu.Unlock()

	now = now.Add(2 * time.Second)
	controller.NotifyKey()
	controller.expire(oldGeneration)

	if !controller.State().Active || len(events) != 1 {
		t.Fatalf("stale timeout changed state: state=%#v events=%#v", controller.State(), events)
	}

	now = now.Add(PetTypingTimeout)
	controller.mu.Lock()
	newGeneration := controller.generation
	controller.mu.Unlock()
	controller.expire(newGeneration)

	want := []PetTypingState{events[0], {Active: false, LastKeyAt: now.Add(-PetTypingTimeout).UnixMilli()}}
	if len(events) != 2 || events[1].Active || !reflect.DeepEqual(events[1], want[1]) {
		t.Fatalf("new timeout events = %#v, want inactive edge %#v", events, want[1])
	}
}

func TestPetTypingAPIReadsControllerState(t *testing.T) {
	controller := NewPetTypingController(PetTypingControllerOptions{})
	defer controller.Close()
	api := NewPetTypingAPI(controller)
	if got := api.State(); got.Active {
		t.Fatalf("initial typing state = %#v, want inactive", got)
	}
	controller.NotifyKey()
	if got := api.State(); !got.Active {
		t.Fatalf("typing API state = %#v, want active", got)
	}
}

func TestPetTypingControllerCloseStopsStateChanges(t *testing.T) {
	events := make([]PetTypingState, 0, 1)
	controller := NewPetTypingController(PetTypingControllerOptions{
		Emit: func(state PetTypingState) { events = append(events, state) },
	})
	controller.Close()
	controller.NotifyKey()
	if len(events) != 0 || controller.State().Active {
		t.Fatalf("closed controller changed state: events=%#v state=%#v", events, controller.State())
	}
}
