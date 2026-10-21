package chat

import (
	"context"
	"errors"
	"testing"
)

func TestTTSTurnTrackerKeepsFirstItemError(t *testing.T) {
	tracker := newTTSTurnTracker()
	first, second := tracker.Add(), tracker.Add()
	if tracker.Err() != nil {
		t.Fatal("new tracker should have no error")
	}

	genErr := errors.New("tts provider missing")
	first(genErr)
	second(errors.New("later error"))
	second(nil) // duplicate calls are ignored

	if err := tracker.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned %v; item errors must not fail the turn itself", err)
	}
	if !errors.Is(tracker.Err(), genErr) {
		t.Fatalf("Err() = %v, want first item error", tracker.Err())
	}
}

func TestTTSTurnTrackerNoErrorWhenItemsSucceed(t *testing.T) {
	tracker := newTTSTurnTracker()
	done := tracker.Add()
	done(nil)
	if tracker.Err() != nil {
		t.Fatalf("Err() = %v, want nil", tracker.Err())
	}
	var nilTracker *ttsTurnTracker
	if nilTracker.Err() != nil {
		t.Fatal("nil tracker must report no error")
	}
}

// A provider that times out may close its output without an error; the turn
// then ends cleanly but nothing was played.
func TestTTSTurnTrackerHadAudio(t *testing.T) {
	tracker := newTTSTurnTracker()
	done := tracker.Add()
	done(nil)
	if tracker.HadAudio() {
		t.Fatal("HadAudio() = true before any frame")
	}

	ctx := context.WithValue(context.Background(), ttsTurnTrackerKey, tracker)
	markTTSTurnAudio(ctx)
	if !tracker.HadAudio() {
		t.Fatal("HadAudio() = false after markTTSTurnAudio")
	}

	var nilTracker *ttsTurnTracker
	nilTracker.MarkAudio() // must not panic
	if nilTracker.HadAudio() {
		t.Fatal("nil tracker must report no audio")
	}
	markTTSTurnAudio(context.Background()) // no tracker in ctx: no-op
}
