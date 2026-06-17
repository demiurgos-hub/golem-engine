package net

import "context"

// reliableMessageChannel is the transport-neutral reliable lane used by Session.
type reliableMessageChannel interface {
	WriteBatch(ctx context.Context, frames [][]byte) error
	ReadMessages(ctx context.Context, onMsg func([]byte)) error
	Close() error
}

// datagramChannel is the raw datagram transport used by Session.
type datagramChannel interface {
	WriteDatagram(ctx context.Context, data []byte) error
	ReadDatagrams(ctx context.Context, onDatagram func([]byte)) error
}
