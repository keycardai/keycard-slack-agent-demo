package app

import "testing"

func TestMakeSessionIDWithThread(t *testing.T) {
	id := MakeSessionID("T1", "C1", "123.456")
	expected := "slack:T1:C1:123.456"
	if id != expected {
		t.Fatalf("expected %q, got %q", expected, id)
	}
}

func TestMakeSessionIDWithoutThread(t *testing.T) {
	id := MakeSessionID("T1", "C1", "")
	expected := "slack:T1:C1"
	if id != expected {
		t.Fatalf("expected %q, got %q", expected, id)
	}
}

func TestMakeSessionIDDifferentThreadsDifferentSessions(t *testing.T) {
	id1 := MakeSessionID("T1", "C1", "111.222")
	id2 := MakeSessionID("T1", "C1", "333.444")
	if id1 == id2 {
		t.Fatalf("expected different session IDs for different threads, got %q", id1)
	}
}

func TestMakeSessionIDDifferentChannelsDifferentSessions(t *testing.T) {
	id1 := MakeSessionID("T1", "C1", "")
	id2 := MakeSessionID("T1", "C2", "")
	if id1 == id2 {
		t.Fatalf("expected different session IDs for different channels, got %q", id1)
	}
}
