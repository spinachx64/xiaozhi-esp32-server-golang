package diagnostics

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureSeparatesDecodedAndSentAudio(t *testing.T) {
	t.Setenv("XIAOZHI_ASR_DEBUG_DIR", t.TempDir())
	ctx, trace := New(context.Background(), "aa:bb", 16000, 1)
	if FromContext(ctx) != trace {
		t.Fatal("trace lost across provider context")
	}
	samples := []float32{-2, 0, 2}
	trace.PCM("decoded", samples)
	samples[0] = 0 // capture must own its samples
	trace.Bytes("sent", []byte{0, 0})
	trace.Event("partial: 退出")
	trace.Event("commit")
	trace.Event("final: 嗯")
	trace.save()
	decoded, err := os.ReadFile(filepath.Join(trace.dir, "decoded.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded[:4]) != "RIFF" || string(decoded[8:12]) != "WAVE" || len(decoded) != 50 {
		t.Fatalf("invalid WAV header or length: %v", decoded)
	}
	if binary.LittleEndian.Uint32(decoded[24:]) != 16000 || binary.LittleEndian.Uint16(decoded[44:]) != uint16(32769) {
		t.Fatal("sample rate or clipped PCM sample incorrect")
	}
	sent, err := os.ReadFile(filepath.Join(trace.dir, "sent.wav"))
	if err != nil || len(sent) != 46 {
		t.Fatalf("sent capture differs from wire bytes: len=%d err=%v", len(sent), err)
	}
	b, err := os.ReadFile(filepath.Join(trace.dir, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Events []event `json:"events"`
	}
	if err := json.Unmarshal(b, &metadata); err != nil || len(metadata.Events) != 3 {
		t.Fatalf("timeline missing: %s err=%v", b, err)
	}
	_, next := New(context.Background(), "aa:bb", 16000, 1)
	if trace.ID == next.ID || len(next.audio) != 0 {
		t.Fatal("turn captures were mixed")
	}
}

func TestCaptureIsBoundedAndDisabledByDefault(t *testing.T) {
	t.Setenv("XIAOZHI_ASR_DEBUG_DIR", "")
	_, disabled := New(context.Background(), "device", 16000, 1)
	disabled.PCM("decoded", []float32{1})
	disabled.Event("ignored")
	if len(disabled.audio) != 0 || len(disabled.events) != 0 {
		t.Fatal("disabled capture retains data")
	}
	t.Setenv("XIAOZHI_ASR_DEBUG_DIR", t.TempDir())
	_, trace := New(context.Background(), "device", 10, 1)
	trace.Bytes("sent", make([]byte, 1400))
	trace.Bytes("sent", []byte{1, 2})
	if len(trace.audio["sent"]) != 1200 || !trace.truncated["sent"] {
		t.Fatal("audio capture exceeded 60 seconds")
	}
	for i := 0; i < 600; i++ {
		trace.Event("partial")
	}
	trace.Event("final")
	if len(trace.events) != 512 || trace.events[511].Message != "final" {
		t.Fatal("bounded timeline lost final event")
	}
	trace.events = nil
	trace.closed = true
	trace.Event("late event")
	trace.Bytes("decoded", []byte{1, 2})
	if len(trace.events) != 0 || len(trace.audio["decoded"]) != 0 {
		t.Fatal("closed capture accepted late writes")
	}
}
