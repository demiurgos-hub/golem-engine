package golemclient

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDialWebSocketAcceptsReliableMessagesLargerThanLibraryDefault(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 64*1024)
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.CloseNow()
		serverErrors <- conn.Write(r.Context(), websocket.MessageBinary, payload)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	channel, err := DialWebSocket(ctx, ConnectOptions{
		Transport: TransportWebSocket,
		URL:       "ws" + strings.TrimPrefix(server.URL, "http"),
	})
	if err != nil {
		t.Fatalf("DialWebSocket: %v", err)
	}
	defer channel.Close()

	received := make(chan []byte, 1)
	channel.OnMessage(func(data []byte) {
		received <- append([]byte(nil), data...)
	})

	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("received payload differs: got %d bytes, want %d", len(got), len(payload))
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for payload: %v", ctx.Err())
	}

	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatalf("server write: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for server write: %v", ctx.Err())
	}
}
