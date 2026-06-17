package net

import (
	"context"
	"fmt"

	"github.com/quic-go/webtransport-go"
)

// webTransportReliableChannel maps the transport-neutral reliable interface
// onto a single long-lived framed WebTransport stream.
type webTransportReliableChannel struct {
	stream       *webtransport.Stream
	writeScratch []byte
}

func newWebTransportReliableChannel(stream *webtransport.Stream) *webTransportReliableChannel {
	return &webTransportReliableChannel{stream: stream}
}

func (c *webTransportReliableChannel) WriteBatch(_ context.Context, frames [][]byte) error {
	scratch, err := writeReliableFrameChunks(frames, maxReliableChunkBytes, lengthPrefixedReliableChunkEncoder, c.writeScratch, func(chunk []byte) error {
		if _, err := c.stream.Write(chunk); err != nil {
			return fmt.Errorf("webtransport reliable stream write: %w", err)
		}
		return nil
	})
	c.writeScratch = scratch
	return err
}

func (c *webTransportReliableChannel) ReadMessages(ctx context.Context, onMsg func([]byte)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	readDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.stream.CancelRead(0)
		case <-readDone:
		}
	}()
	defer close(readDone)

	for {
		data, err := readReliableFrame(c.stream)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("webtransport reliable stream read: %w", err)
		}
		if onMsg != nil {
			onMsg(data)
		}
	}
}

func (c *webTransportReliableChannel) Close() error {
	c.stream.CancelRead(0)
	c.stream.CancelWrite(0)
	return c.stream.Close()
}

// webTransportDatagramChannel maps the raw datagram transport interface onto
// WebTransport datagrams.
type webTransportDatagramChannel struct {
	session *webtransport.Session
}

func newWebTransportDatagramChannel(session *webtransport.Session) *webTransportDatagramChannel {
	return &webTransportDatagramChannel{session: session}
}

func (c *webTransportDatagramChannel) WriteDatagram(_ context.Context, data []byte) error {
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	if err := c.session.SendDatagram(data); err != nil {
		return fmt.Errorf("webtransport datagram write: %w", err)
	}
	return nil
}

func (c *webTransportDatagramChannel) ReadDatagrams(ctx context.Context, onDatagram func([]byte)) error {
	for {
		data, err := c.session.ReceiveDatagram(ctx)
		if err != nil {
			return fmt.Errorf("webtransport datagram read: %w", err)
		}
		if err := validateDatagramSize(data); err != nil {
			return fmt.Errorf("webtransport datagram read: %w", err)
		}
		if onDatagram != nil {
			onDatagram(data)
		}
	}
}
