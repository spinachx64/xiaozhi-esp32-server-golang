//go:build livebridge

package delegate

import (
	"context"
	"os"
	"testing"
	"time"
)

// Temporary end-to-end check against a running agent-bridge + Codex.
func TestLiveBridge(t *testing.T) {
	c, err := NewClient(os.Getenv("LIVE_BRIDGE_URL"), os.Getenv("LIVE_BRIDGE_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	dev := "aa:bb:cc:dd:ee:01"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1. Submit through the manager, as /api/tasks does, and wait for the notify.
	m := NewManager(c, 3*time.Minute, 1)
	defer m.Close()
	got := make(chan struct{}, 4)
	detach := m.Attach(dev, func() { got <- struct{}{} })
	id, err := m.Submit(dev, "用一句话回答：1加1等于几？", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("submitted %s", id)
	select {
	case <-got:
	case <-ctx.Done():
		t.Fatal("no result in time")
	}
	r, ok := m.TakePending(dev)
	if !ok || r.Err != nil || r.Text == "" {
		t.Fatalf("bad result: ok=%v err=%v text=%q", ok, r.Err, r.Text)
	}
	t.Logf("result: %s", r.Text)
	detach()

	// 2. Not acked yet: the bridge must still hold it (offline recovery path).
	stored, err := c.Fetch(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stored {
		found = found || s.TaskID == id
	}
	if !found {
		t.Fatalf("bridge did not keep unacked result %s: %+v", id, stored)
	}

	// 3. Ack after "playback", then the bridge drops it.
	if err := c.Ack(ctx, dev, []string{id}); err != nil {
		t.Fatal(err)
	}
	stored, err = c.Fetch(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stored {
		if s.TaskID == id {
			t.Fatalf("result %s still stored after ack", id)
		}
	}
}
