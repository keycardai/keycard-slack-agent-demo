package agent

import "testing"

func TestShouldPostToolOutputBool(t *testing.T) {
	if !shouldPostToolOutput(map[string]any{"post_output": true}) {
		t.Fatal("expected true for bool true")
	}
	if shouldPostToolOutput(map[string]any{"post_output": false}) {
		t.Fatal("expected false for bool false")
	}
}

func TestShouldPostToolOutputString(t *testing.T) {
	if !shouldPostToolOutput(map[string]any{"post_output": "true"}) {
		t.Fatal("expected true for string 'true'")
	}
	if !shouldPostToolOutput(map[string]any{"post_output": "TRUE"}) {
		t.Fatal("expected true for string 'TRUE'")
	}
	if shouldPostToolOutput(map[string]any{"post_output": "false"}) {
		t.Fatal("expected false for string 'false'")
	}
}

func TestShouldPostToolOutputFloat(t *testing.T) {
	if !shouldPostToolOutput(map[string]any{"post_output": float64(1)}) {
		t.Fatal("expected true for float64(1)")
	}
	if shouldPostToolOutput(map[string]any{"post_output": float64(0)}) {
		t.Fatal("expected false for float64(0)")
	}
}

func TestShouldPostToolOutputMissing(t *testing.T) {
	if shouldPostToolOutput(map[string]any{}) {
		t.Fatal("expected false when key is missing")
	}
	if shouldPostToolOutput(nil) {
		t.Fatal("expected false for nil input")
	}
}

func TestSplitSlackChunksSingleChunk(t *testing.T) {
	chunks := splitSlackChunks("hello", 100)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Fatalf("expected single chunk, got %v", chunks)
	}
}

func TestSplitSlackChunksMultipleChunks(t *testing.T) {
	chunks := splitSlackChunks("abcdefghij", 3)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != "abc" || chunks[1] != "def" || chunks[2] != "ghi" || chunks[3] != "j" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
}

func TestSplitSlackChunksExactBoundary(t *testing.T) {
	chunks := splitSlackChunks("abcdef", 3)
	if len(chunks) != 2 || chunks[0] != "abc" || chunks[1] != "def" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
}

func TestSplitSlackChunksZeroMax(t *testing.T) {
	chunks := splitSlackChunks("hello", 0)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Fatalf("expected single chunk for zero max, got %v", chunks)
	}
}
