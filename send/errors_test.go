package send

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyNil(t *testing.T) {
	if Classify(nil) != nil {
		t.Error("Classify(nil) must be nil")
	}
}

func TestClassifyBotWideByMarker(t *testing.T) {
	err := fmt.Errorf("send failed: %s", "the store doesn't contain a device JID")
	got := Classify(err)
	if !errors.Is(got, ErrBotWide) {
		t.Errorf("expected ErrBotWide, got %v", got)
	}
	if errors.Is(got, ErrPeerUnreachable) {
		t.Error("must not also be ErrPeerUnreachable")
	}
}

func TestClassifyUnknownPassesThrough(t *testing.T) {
	err := errors.New("some transient thing")
	got := Classify(err)
	if errors.Is(got, ErrBotWide) || errors.Is(got, ErrPeerUnreachable) {
		t.Errorf("unknown error must not be classified: %v", got)
	}
	if !errors.Is(got, err) {
		t.Error("original error must remain wrapped")
	}
}
