package net

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDatagramSendValidationPrecedence(t *testing.T) {
	oversize := make([]byte, maxReliableMessageBytes+1)
	for _, tc := range []struct {
		name        string
		unsupported error
		send        func(*Listener, int64, []byte) error
	}{
		{"unreliable", ErrUnreliableNotSupported, (*Listener).SendUnreliable},
		{"unordered", ErrReliableDatagramsNotSupported, (*Listener).SendReliableUnordered},
		{"ordered", ErrReliableDatagramsNotSupported, (*Listener).SendReliableOrdered},
		{"ordered batch", ErrReliableDatagramsNotSupported, func(l *Listener, id int64, b []byte) error { return l.SendReliableOrderedBatch(id, [][]byte{b}) }},
		{"raw batch", ErrUnreliableNotSupported, func(l *Listener, id int64, b []byte) error { return l.SendUnreliableStateBatch(id, [][]byte{b}) }},
		{"raw owned", ErrUnreliableNotSupported, (*Listener).SendUnreliableStateOwned},
		{"eventual", ErrReliableDatagramsNotSupported, func(l *Listener, id int64, b []byte) error { return l.SendEventualState(id, 1, b) }},
		{"eventual owned", ErrReliableDatagramsNotSupported, func(l *Listener, id int64, b []byte) error { return l.SendEventualStateOwned(id, 1, b) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := NewListener(nil, Config{Transport: TransportWebSocket})
			if err := tc.send(ws, 999, oversize); !errors.Is(err, tc.unsupported) {
				t.Fatalf("config before lookup/size: %v", err)
			}
			mixed := NewListener(nil, Config{Transport: TransportWebTransport})
			if err := tc.send(mixed, 999, oversize); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("lookup before size: %v", err)
			}
			fallback := newSession(1, &captureReliableChannel{}, nil, nil)
			fallback.Transport = TransportWebTransport // Public metadata must not enable capabilities.
			mixed.addSession(fallback)
			t.Cleanup(func() { mixed.removeSession(fallback) })
			if err := tc.send(mixed, 1, oversize); !errors.Is(err, tc.unsupported) {
				t.Fatalf("capability before size: %v", err)
			}
			capable := newSession(2, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
			mixed.addSession(capable)
			t.Cleanup(func() { mixed.removeSession(capable) })
			if err := tc.send(mixed, 2, oversize); err == nil || !strings.Contains(err.Error(), "exceeds max") {
				t.Fatalf("capable validation: %v", err)
			}
			if capable.IsClosing() {
				t.Fatal("listener validation failure closed session")
			}
		})
	}
	// Stream sends deliberately validate before session lookup.
	l := NewListener(nil, Config{})
	if err := l.SendWrapped(999, oversize); err == nil || errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("stream size precedence: %v", err)
	}
	// Broadcasts validate even with no capable recipients.
	l = NewListener(nil, Config{Transport: TransportWebTransport})
	if err := l.BroadcastReliableOrdered(oversize); err == nil {
		t.Fatal("empty broadcast skipped validation")
	}
}

func TestAllOutboundQueuesCloseOnceWithoutWaiting(t *testing.T) {
	for _, lane := range []string{"stream", "unreliable", "raw", "eventual", "ordered", "unordered"} {
		t.Run(lane, func(t *testing.T) {
			var closes atomic.Int32
			release := make(chan struct{})
			s := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, func() error { closes.Add(1); <-release; return nil })
			t.Cleanup(func() {
				close(release)
				s.closeNow()
				if closes.Load() != 1 {
					t.Fatalf("transport close count = %d", closes.Load())
				}
			})
			enqueue := func() {
				switch lane {
				case "stream":
					s.queueStream([][]byte{[]byte("x")})
				case "unreliable":
					s.queueUnreliable([]byte("x"))
				case "raw":
					s.queueRawState([]byte("x"))
				case "eventual":
					s.queueEventualState(eventualStateDatagram{payload: []byte("x")})
				case "ordered":
					_ = s.queueReliable(datagramLaneReliableOrdered, []byte("x"))
				case "unordered":
					_ = s.queueReliable(datagramLaneReliableUnordered, []byte("x"))
				}
			}
			for i := 0; i < sendBufSize; i++ {
				enqueue()
			}
			if s.IsClosing() {
				t.Fatal("queue closed before capacity")
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); enqueue() }()
			}
			returned := make(chan struct{})
			go func() { wg.Wait(); close(returned) }()
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("overflow blocked on close")
			}
			if !s.IsClosing() {
				t.Fatal("overflow did not fence sends")
			}
			before := s.OutboundSnapshot(time.Now())
			enqueue()
			after := s.OutboundSnapshot(time.Now())
			if before.StreamBatchesQueued != after.StreamBatchesQueued || before.UnreliableQueued != after.UnreliableQueued || before.PendingOrdered != after.PendingOrdered || before.PendingUnordered != after.PendingUnordered || before.EventualQueued != after.EventualQueued {
				t.Fatal("closing queue accepted more work")
			}
			s.RequestCloseWithReason("later")
			if s.requestedCloseReason() != "" {
				t.Fatal("later close replaced first reason")
			}
		})
	}
}

type contractDatagramWriter struct {
	calls int
	err   error
}

func (c *contractDatagramWriter) WriteDatagram(context.Context, []byte) error {
	c.calls++
	return c.err
}
func (c *contractDatagramWriter) ReadDatagrams(context.Context, func([]byte)) error { return nil }

func TestPhysicalDatagramCountsAndErrors(t *testing.T) {
	for _, raw := range []bool{false, true} {
		writer := &contractDatagramWriter{}
		s := newSession(1, &captureReliableChannel{}, writer, nil)
		write := s.writeProtocolDatagram
		unsupported := ErrReliableDatagramsNotSupported
		prefix := "protocol datagram write: "
		if raw {
			write = s.writeRawStateDatagram
			unsupported = ErrUnreliableNotSupported
			prefix = "raw state datagram write: "
		}
		if err := write(context.Background(), nil); err != nil || s.wireDatagramOK.Load() != 1 {
			t.Fatalf("successful physical write: %v", err)
		}
		writer.err = errors.New("write failed")
		if err := write(context.Background(), nil); !errors.Is(err, writer.err) || err.Error() != prefix+writer.err.Error() {
			t.Fatalf("write error: %v", err)
		}
		if s.wireDatagramOK.Load() != 1 || writer.calls != 2 {
			t.Fatal("failed write was counted")
		}
		s.datagrams = nil
		if err := write(context.Background(), nil); err != unsupported || s.wireDatagramOK.Load() != 1 {
			t.Fatalf("unsupported write: %v", err)
		}
	}
}

func TestTrafficSamplesWrapAndSessionRemoval(t *testing.T) {
	l := NewListener(nil, Config{})
	s := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	l.addSession(s)
	max := ^uint64(0)
	if got := l.sampleTrafficCounters(1, trafficCounters{max, max, max, max}); got != (trafficCounters{}) {
		t.Fatalf("first sample: %+v", got)
	}
	if got := l.sampleTrafficCounters(1, trafficCounters{3, 4, 5, 6}); got != (trafficCounters{4, 5, 6, 7}) {
		t.Fatalf("wrapped sample: %+v", got)
	}
	l.removeSession(s)
	if len(l.trafficPrev) != 0 {
		t.Fatal("session removal retained counter history")
	}
	if got := l.sampleTrafficCounters(1, trafficCounters{9, 9, 9, 9}); got != (trafficCounters{}) {
		t.Fatalf("reused ID inherited counters: %+v", got)
	}
}
