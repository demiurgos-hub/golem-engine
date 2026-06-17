package net

import (
	"context"
	"fmt"
	"log"

	"github.com/coder/websocket"
)

// websocketReliableChannel maps the transport-neutral reliable interface onto a
// single WebSocket connection.
type websocketReliableChannel struct {
	conn         *websocket.Conn
	writeScratch []byte
}

func newWebSocketReliableChannel(conn *websocket.Conn) *websocketReliableChannel {
	return &websocketReliableChannel{conn: conn}
}

func (c *websocketReliableChannel) WriteBatch(ctx context.Context, frames [][]byte) error {
	scratch, err := writeReliableFrameChunks(frames, maxWebSocketPayloadBytes, rawReliableChunkEncoder, c.writeScratch, func(chunk []byte) error {
		if err := c.conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
			return fmt.Errorf("websocket reliable write: %w", err)
		}
		return nil
	})
	c.writeScratch = scratch
	return err
}

func (c *websocketReliableChannel) ReadMessages(ctx context.Context, onMsg func([]byte)) error {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("websocket reliable read: %w", err)
		}
		if err := validateWebSocketPayloadSize(data); err != nil {
			return fmt.Errorf("websocket reliable read: %w", err)
		}
		if onMsg != nil {
			onMsg(data)
		}
	}
}

func (c *websocketReliableChannel) Close() error {
	c.conn.CloseNow()
	return nil
}

func logReliableWriteDrop(sessionID int64, err error) error {
	log.Printf("golem/net: session %d dropping oversized queued frame: %v", sessionID, err)
	return fmt.Errorf("session %d: %w", sessionID, err)
}
