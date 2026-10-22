package qwen

import (
	"crypto/tls"
	"fmt"
	"net/http/httptrace"
	"sync"
	"time"
)

// requestTrace records how a TTS request got its connection and when the
// first response byte arrived, so header timeouts can be told apart: a reused
// connection that sat idle for long (likely silently dropped by NAT/proxy)
// versus a fresh or just-used connection where the server itself was slow.
type requestTrace struct {
	start time.Time

	mu        sync.Mutex
	gotConn   time.Duration
	reused    bool
	wasIdle   bool
	idleTime  time.Duration
	connected bool
	dnsDone   time.Duration
	connDone  time.Duration
	tlsDone   time.Duration
	wrote     time.Duration
	firstByte time.Duration
}

func (t *requestTrace) since() time.Duration { return time.Since(t.start) }

func (t *requestTrace) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			t.dnsDone = t.since()
			t.mu.Unlock()
		},
		ConnectDone: func(_, _ string, err error) {
			if err != nil {
				return
			}
			t.mu.Lock()
			t.connDone = t.since()
			t.mu.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				return
			}
			t.mu.Lock()
			t.tlsDone = t.since()
			t.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.connected = true
			t.gotConn = t.since()
			t.reused = info.Reused
			t.wasIdle = info.WasIdle
			t.idleTime = info.IdleTime
			t.mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			t.mu.Lock()
			t.wrote = t.since()
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			t.mu.Lock()
			t.firstByte = t.since()
			t.mu.Unlock()
		},
	}
}

func ms(d time.Duration) int64 { return d.Milliseconds() }

func (t *requestTrace) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.connected {
		return fmt.Sprintf("conn=none elapsed=%dms", ms(t.since()))
	}
	conn := "new"
	if t.reused {
		conn = "reused"
	}
	s := fmt.Sprintf("conn=%s got_conn=%dms", conn, ms(t.gotConn))
	if t.wasIdle {
		s += fmt.Sprintf(" idle_before=%dms", ms(t.idleTime))
	}
	if !t.reused {
		s += fmt.Sprintf(" dns=%dms connect=%dms tls=%dms", ms(t.dnsDone), ms(t.connDone), ms(t.tlsDone))
	}
	s += fmt.Sprintf(" wrote=%dms", ms(t.wrote))
	if t.firstByte > 0 {
		s += fmt.Sprintf(" first_byte=%dms", ms(t.firstByte))
	} else {
		s += fmt.Sprintf(" first_byte=none elapsed=%dms", ms(t.since()))
	}
	return s
}
