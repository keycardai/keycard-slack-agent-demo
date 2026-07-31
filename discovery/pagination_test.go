package discovery

import (
	"context"
	"strings"
	"testing"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

func TestCollectPagesWalksEveryPage(t *testing.T) {
	pages := map[string]*keycard.Page[keycard.Application]{
		"":   {Items: []keycard.Application{{ID: "app_1"}}, Pagination: keycard.Pagination{AfterCursor: "p2"}},
		"p2": {Items: []keycard.Application{{ID: "app_2"}}},
	}
	got, err := collectPages(context.Background(), func(_ context.Context, after string) (*keycard.Page[keycard.Application], error) {
		return pages[after], nil
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 2 || got[0].ID != "app_1" || got[1].ID != "app_2" {
		t.Fatalf("unexpected items: %#v", got)
	}
}

func TestCollectPagesStopsOnARepeatingCursor(t *testing.T) {
	// The failure this guards against: an API that returns the same cursor on the last page turns
	// "loop until the cursor is empty" into a loop that never ends. It has to fail loudly instead.
	calls := 0
	page := &keycard.Page[keycard.Application]{
		Items:      []keycard.Application{{ID: "app_1"}},
		Pagination: keycard.Pagination{AfterCursor: "same"},
	}
	_, err := collectPages(context.Background(), func(_ context.Context, _ string) (*keycard.Page[keycard.Application], error) {
		calls++
		return page, nil
	})
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("expected a stalled-cursor error, got %v", err)
	}
	// Two calls: the first page, then the one that repeats the cursor.
	if calls != 2 {
		t.Fatalf("expected the loop to stop after 2 calls, made %d", calls)
	}
}

func TestCollectPagesRefusesToRunForever(t *testing.T) {
	// A cursor that keeps changing but never empties is the other unbounded case.
	calls := 0
	_, err := collectPages(context.Background(), func(_ context.Context, after string) (*keycard.Page[keycard.Application], error) {
		calls++
		return &keycard.Page[keycard.Application]{
			Items:      []keycard.Application{{ID: "app"}},
			Pagination: keycard.Pagination{AfterCursor: after + "x"},
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected a page-cap error, got %v", err)
	}
	if calls != maxInventoryPages {
		t.Fatalf("expected exactly %d calls, made %d", maxInventoryPages, calls)
	}
}

func TestCollectPagesPropagatesErrors(t *testing.T) {
	_, err := collectPages(context.Background(), func(_ context.Context, _ string) (*keycard.Page[keycard.Application], error) {
		return nil, context.DeadlineExceeded
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected the fetch error to propagate, got %v", err)
	}
}
