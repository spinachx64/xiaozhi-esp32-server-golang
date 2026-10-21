package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"xiaozhi-esp32-server-golang/internal/app/server/chat"
	"xiaozhi-esp32-server-golang/internal/domain/delegate"
)

// A device that answers "busy" (the user started talking right after the idle
// check) keeps the result and gets it once it accepts; busy is not a failure.
func TestDeliveryRetriesWhenDeviceBusy(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	dev1 := &fakeDevice{busyReplies: delegateMaxInjectFails + 1}
	table.set(dev, dev1)
	detach := d.attach(dev)
	defer detach()

	if _, err := mgr.Submit(dev, "任务", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "announcement after busy replies", func() bool { return dev1.count() == 1 })
	eventually(t, "ack", func() bool { return len(bridge.ackedIDs()) == 1 })
}

// fakeBridge runs tasks instantly (or when released) and records fetch/ack.
type fakeBridge struct {
	mu      sync.Mutex
	release chan struct{} // nil: answer at once
	stored  []delegate.StoredResult
	acked   []string
}

func (b *fakeBridge) Run(ctx context.Context, task delegate.Task, _ func(string)) (string, error) {
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "答案:" + task.Text, nil
}

func (b *fakeBridge) Fetch(context.Context, string) ([]delegate.StoredResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]delegate.StoredResult(nil), b.stored...), nil
}

func (b *fakeBridge) Ack(_ context.Context, _ string, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acked = append(b.acked, ids...)
	return nil
}

func (b *fakeBridge) ackedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.acked...)
}

// fakeDevice plays announcements; plays lists the outcome of each one
// (true when nothing is left).
type fakeDevice struct {
	mu       sync.Mutex
	busy     bool
	plays    []bool
	injected []string
	busySeen bool // an injection happened while busy

	busyReplies int // next injections answer speak_ready "busy"
}

func (f *fakeDevice) IsConversationActive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy
}

func (f *fakeDevice) InjectDelegateSpeech(text string, onSpoken func(bool)) error {
	f.mu.Lock()
	if f.busyReplies > 0 {
		f.busyReplies--
		f.mu.Unlock()
		return chat.ErrSpeakBusy
	}
	if f.busy {
		f.busySeen = true
	}
	f.injected = append(f.injected, text)
	played := true
	if len(f.plays) > 0 {
		played, f.plays = f.plays[0], f.plays[1:]
	}
	f.mu.Unlock()
	go onSpoken(played)
	return nil
}

func (f *fakeDevice) setBusy(b bool) {
	f.mu.Lock()
	f.busy = b
	f.mu.Unlock()
}

func (f *fakeDevice) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.injected)
}

type deviceTable struct {
	mu   sync.Mutex
	devs map[string]*fakeDevice
}

func (t *deviceTable) set(id string, d *fakeDevice) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d == nil {
		delete(t.devs, id)
	} else {
		t.devs[id] = d
	}
}

func (t *deviceTable) lookup(id string) (speechTarget, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.devs[id]
	return d, ok
}

func setup(t *testing.T, bridge *fakeBridge) (*delegateDelivery, *deviceTable, *delegate.Manager) {
	t.Helper()
	oldPoll, oldRetry := delegateIdlePoll, delegateRetryDelay
	delegateIdlePoll, delegateRetryDelay = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { delegateIdlePoll, delegateRetryDelay = oldPoll, oldRetry })

	mgr := delegate.NewManager(bridge, time.Minute, 2)
	t.Cleanup(mgr.Close)
	table := &deviceTable{devs: map[string]*fakeDevice{}}
	return newDelegateDeliveryWith(mgr, table.lookup), table, mgr
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

const dev = "aa:bb:cc:dd:ee:ff"

func TestDeliveryAnnouncesWhenIdleAndAcks(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	device := &fakeDevice{}
	table.set(dev, device)
	defer d.attach(dev)()

	id, err := mgr.Submit(dev, "查天气", "")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "ack", func() bool { return len(bridge.ackedIDs()) == 1 })
	if got := bridge.ackedIDs()[0]; got != id {
		t.Fatalf("acked %s, want %s", got, id)
	}
	if device.count() != 1 {
		t.Fatalf("injected %d times, want 1", device.count())
	}
}

func TestDeliveryWaitsForConversationToEnd(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	device := &fakeDevice{busy: true}
	table.set(dev, device)
	defer d.attach(dev)()

	if _, err := mgr.Submit(dev, "查天气", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if device.count() != 0 {
		t.Fatal("announced during a conversation")
	}
	device.setBusy(false)
	eventually(t, "ack after idle", func() bool { return len(bridge.ackedIDs()) == 1 })
	if device.busySeen {
		t.Fatal("injected while busy")
	}
}

func TestDeliveryRetriesInterruptedAnnouncement(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	device := &fakeDevice{plays: []bool{false}} // first one interrupted
	table.set(dev, device)
	defer d.attach(dev)()

	if _, err := mgr.Submit(dev, "查天气", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ack after replay", func() bool { return len(bridge.ackedIDs()) == 1 })
	if device.count() != 2 {
		t.Fatalf("injected %d times, want 2", device.count())
	}
}

func TestDeliveryGivesUpAfterRepeatedInterrupts(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	device := &fakeDevice{plays: []bool{false, false, false, false, false}}
	table.set(dev, device)
	defer d.attach(dev)()

	if _, err := mgr.Submit(dev, "查天气", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "max interrupts", func() bool { return device.count() == delegateMaxInterrupted })
	time.Sleep(100 * time.Millisecond)
	if device.count() != delegateMaxInterrupted || len(bridge.ackedIDs()) != 0 {
		t.Fatalf("injected %d, acked %d; want %d and 0", device.count(), len(bridge.ackedIDs()), delegateMaxInterrupted)
	}
	if !mgr.HasPending(dev) {
		t.Fatal("result should stay pending for the next connection")
	}
}

func TestDeliveryAfterReconnect(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)

	// Device is offline when the task finishes.
	if _, err := mgr.Submit(dev, "查天气", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pending", func() bool { return mgr.HasPending(dev) })

	device := &fakeDevice{}
	table.set(dev, device)
	defer d.attach(dev)()
	eventually(t, "ack after reconnect", func() bool { return len(bridge.ackedIDs()) == 1 })
}

func TestDeliveryStopsWhenDeviceDisconnects(t *testing.T) {
	bridge := &fakeBridge{}
	d, table, mgr := setup(t, bridge)
	device := &fakeDevice{busy: true}
	table.set(dev, device)
	detach := d.attach(dev)

	if _, err := mgr.Submit(dev, "查天气", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	table.set(dev, nil)
	detach()
	eventually(t, "loop exit", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return !d.active[dev]
	})
	if !mgr.HasPending(dev) || device.count() != 0 {
		t.Fatal("result should wait for the next connection")
	}

	again := &fakeDevice{}
	table.set(dev, again)
	defer d.attach(dev)()
	eventually(t, "ack on the new connection", func() bool { return len(bridge.ackedIDs()) == 1 })
}

func TestDeliveryFetchesStoredResultsOnce(t *testing.T) {
	bridge := &fakeBridge{stored: []delegate.StoredResult{
		{TaskID: "old1", Task: "旧任务", Text: "旧答案", FinishedAt: time.Now()},
	}}
	d, table, _ := setup(t, bridge)
	device := &fakeDevice{}
	table.set(dev, device)

	detach := d.attach(dev)
	eventually(t, "stored result acked", func() bool { return len(bridge.ackedIDs()) == 1 })
	detach()

	// The bridge may still return it if a fetch races the ack; it must not repeat.
	detach = d.attach(dev)
	defer detach()
	time.Sleep(100 * time.Millisecond)
	if device.count() != 1 {
		t.Fatalf("stored result announced %d times, want 1", device.count())
	}
}
