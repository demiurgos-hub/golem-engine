//go:build !js || !wasm

package golemclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
	"github.com/quic-go/webtransport-go"
)

// This stream models the dependency's session-gone wait: stream cancellation
// does not release I/O after it has left the session registry, but deadlines do.
type sessionGoneStream struct {
	mu                                         sync.Mutex
	readDeadline, writeDeadline                time.Time
	readNotify, writeNotify                    chan struct{}
	readStarted, writeStarted                  chan struct{}
	readOnce, writeOnce                        sync.Once
	blockedWrite                               bool
	written                                    bytes.Buffer
	readersCancelled, writersCancelled, closed atomic.Int32
	writers, maxWriters                        atomic.Int32
}

func newSessionGoneStream(blockedWrite bool) *sessionGoneStream {
	return &sessionGoneStream{readNotify: make(chan struct{}, 1), writeNotify: make(chan struct{}, 1), readStarted: make(chan struct{}), writeStarted: make(chan struct{}), blockedWrite: blockedWrite}
}
func (s *sessionGoneStream) wait(read bool) error {
	for {
		s.mu.Lock()
		deadline, notify := s.writeDeadline, s.writeNotify
		if read {
			deadline, notify = s.readDeadline, s.readNotify
		}
		s.mu.Unlock()
		if deadline.IsZero() {
			<-notify
			continue
		}
		delay := time.Until(deadline)
		if delay <= 0 {
			return os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			return os.ErrDeadlineExceeded
		case <-notify:
			timer.Stop()
		}
	}
}
func (s *sessionGoneStream) Read([]byte) (int, error) {
	s.readOnce.Do(func() { close(s.readStarted) })
	return 0, s.wait(true)
}
func (s *sessionGoneStream) Write(p []byte) (int, error) {
	writers := s.writers.Add(1)
	defer s.writers.Add(-1)
	for old := s.maxWriters.Load(); writers > old; old = s.maxWriters.Load() {
		if s.maxWriters.CompareAndSwap(old, writers) {
			break
		}
	}
	s.writeOnce.Do(func() { close(s.writeStarted) })
	if s.blockedWrite {
		return 0, s.wait(false)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.Write(p)
}
func (s *sessionGoneStream) SetReadDeadline(d time.Time) error {
	s.mu.Lock()
	s.readDeadline = d
	s.mu.Unlock()
	select {
	case s.readNotify <- struct{}{}:
	default:
	}
	return nil
}
func (s *sessionGoneStream) SetWriteDeadline(d time.Time) error {
	s.mu.Lock()
	s.writeDeadline = d
	s.mu.Unlock()
	select {
	case s.writeNotify <- struct{}{}:
	default:
	}
	return nil
}
func (s *sessionGoneStream) CancelRead(webtransport.StreamErrorCode)  { s.readersCancelled.Add(1) }
func (s *sessionGoneStream) CancelWrite(webtransport.StreamErrorCode) { s.writersCancelled.Add(1) }
func (s *sessionGoneStream) Close() error                             { s.closed.Add(1); return nil }

func shutdownTestChannel(s *sessionGoneStream) *WebTransportChannel {
	ch := &WebTransportChannel{stream: s, done: make(chan struct{})}
	ch.protocol = newDatagramProtocol(func([]byte) error { return nil }, 0)
	ch.connected.Store(true)
	return ch
}
func awaitShutdown(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestWebTransportCloseInterruptsSessionGoneIO(t *testing.T) {
	s := newSessionGoneStream(true)
	ch := shutdownTestChannel(s)
	// Keep failed assertions from leaking the intentionally blocked test I/O.
	defer s.SetReadDeadline(time.Now())
	defer s.SetWriteDeadline(time.Now())
	readDone, sendDone, schedulerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { defer close(readDone); ch.readLoop(context.Background()) }()
	go func() { defer close(sendDone); _ = ch.Send([]byte("in flight")) }()
	go func() { defer close(schedulerDone); ch.schedulerLoop() }()
	awaitShutdown(t, s.readStarted, "blocked reader")
	awaitShutdown(t, s.writeStarted, "blocked writer")
	var notifications atomic.Int32
	ch.OnClose(func(DisconnectInfo) { notifications.Add(1) })
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); _ = ch.Close() }()
	awaitShutdown(t, closeDone, "bounded close")
	awaitShutdown(t, sendDone, "writer exit")
	awaitShutdown(t, readDone, "reader exit")
	awaitShutdown(t, schedulerDone, "scheduler exit")
	_ = ch.Close()
	if ch.Connected() || notifications.Load() != 0 {
		t.Fatal("local close changed callback contract")
	}
	if s.maxWriters.Load() != 1 {
		t.Fatalf("concurrent framed writers: %d", s.maxWriters.Load())
	}
	if s.readersCancelled.Load() != 1 || s.writersCancelled.Load() != 1 || s.closed.Load() != 1 {
		t.Fatal("physical stream cleanup was not exactly once")
	}
}

func TestWebTransportTerminalErrorCleansUpBeforeReentrantCallback(t *testing.T) {
	s := newSessionGoneStream(true)
	ch := shutdownTestChannel(s)
	defer s.SetWriteDeadline(time.Now())
	sendDone := make(chan struct{})
	go func() { defer close(sendDone); _ = ch.Send([]byte("in flight")) }()
	awaitShutdown(t, s.writeStarted, "blocked writer")
	terminal := &webtransport.SessionError{Remote: true, ErrorCode: 7, Message: "logout"}
	var received DisconnectInfo
	var count atomic.Int32
	ch.OnClose(func(info DisconnectInfo) { received = info; count.Add(1); _ = ch.Close() })
	errorDone := make(chan struct{})
	go func() { defer close(errorDone); ch.closeWithError(terminal) }()
	awaitShutdown(t, errorDone, "terminal cleanup and reentrant callback")
	awaitShutdown(t, sendDone, "writer exit")
	ch.closeWithError(io.EOF)
	if count.Load() != 1 || !errors.Is(received.Err, terminal) {
		t.Fatalf("lost first terminal error: %+v, count=%d", received, count.Load())
	}
	if s.closed.Load() != 1 {
		t.Fatal("terminal failure did not clean up stream")
	}
}

func TestWebTransportCloseKeepsControlFrameAndFencesQueuedSend(t *testing.T) {
	s := newSessionGoneStream(false)
	ch := shutdownTestChannel(s)
	// Race a queued sender against shutdown while the framed writer is held.
	ch.writeMu.Lock()
	sendDone := make(chan struct{})
	go func() { defer close(sendDone); _ = ch.Send([]byte("must not be sent")) }()
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); _ = ch.Close() }()
	awaitShutdown(t, ch.done, "closing fence")
	ch.writeMu.Unlock()
	awaitShutdown(t, sendDone, "queued sender")
	awaitShutdown(t, closeDone, "graceful close")
	payload, err := readReliableFrame(&s.written)
	if err != nil || !bytes.Equal(payload, netwire.AppendCloseControl(nil)) {
		t.Fatalf("close frame=%x err=%v", payload, err)
	}
	if s.written.Len() != 0 {
		t.Fatal("queued application frame written after closing fence")
	}
}

func TestWebTransportCloseAfterPeerLogout(t *testing.T) {
	listener := golemnet.NewListener(registry.NewRegistry(), golemnet.Config{Transport: golemnet.TransportWebTransport, Path: "/wt"})
	accepted := make(chan *golemnet.Session, 1)
	listener.OnConnect(func(s *golemnet.Session) { accepted <- s })
	transportURL := startGolemNetWebTransportListener(t, listener)
	// Exercise the actual stream/session close race, alongside the deterministic
	// orphaned-wait regression above, without depending on race timing to pass.
	for i := 0; i < 16; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ch, err := DialWebTransport(ctx, ConnectOptions{Transport: TransportWebTransport, URL: transportURL, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		closed := make(chan struct{}, 1)
		ch.OnClose(func(DisconnectInfo) { closed <- struct{}{} })
		readDone := make(chan struct{})
		ch.readOnce.Do(func() { go func() { defer close(readDone); ch.readLoop(context.Background()) }() })
		var session *golemnet.Session
		select {
		case session = <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not accept")
		}
		session.RequestCloseWithReason("logout")
		awaitShutdown(t, closed, "peer disconnect callback")
		closeDone := make(chan struct{})
		go func() { defer close(closeDone); _ = ch.Close() }()
		awaitShutdown(t, closeDone, "close after peer logout")
		awaitShutdown(t, readDone, "peer-close reader exit")
		awaitShutdown(t, ch.session.Context().Done(), "physical session shutdown")
	}
}
