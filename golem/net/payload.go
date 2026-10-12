package net

import (
	"errors"
	"fmt"
	"io"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// ErrSessionNotFound reports that a send target disconnected before delivery.
var ErrSessionNotFound = errors.New("golem/net: session not found")

// ErrUnreliableNotSupported reports that the active transport has no datagram lane.
var ErrUnreliableNotSupported = errors.New("golem/net: unreliable transport not supported")

// ErrReliableDatagramsNotSupported reports that the active transport has no reliable datagram lanes.
var ErrReliableDatagramsNotSupported = errors.New("golem/net: reliable datagrams not supported")

// Transport selects the integrated network transport used by the listener.
type Transport string

const (
	// TransportWebSocket serves message-oriented reliable traffic over WebSocket.
	TransportWebSocket Transport = "websocket"
	// TransportWebTransport serves reliable traffic over a framed stream and
	// optional unreliable traffic over datagrams.
	TransportWebTransport Transport = "webtransport"
)

const (
	maxReliableMessageBytes                  = netwire.MaxReliableMessageBytes
	maxReliableChunkBytes                    = 32000
	maxWebSocketPayloadBytes                 = maxReliableMessageBytes
	maxWebTransportDatagramBytes             = netwire.MaxDatagramBytes
	reliableFrameHeaderBytes                 = netwire.FrameHeaderBytes
	maxUnreliableStateDatagramPayloadBytes   = maxWebTransportDatagramBytes
	maxUnreliableDatagramPayloadBytes        = netwire.MaxDatagramBytes - netwire.UnreliableOverhead
	maxReliableUnorderedDatagramPayloadBytes = netwire.MaxDatagramBytes - netwire.ReliableUnorderedOverhead
	maxReliableOrderedDatagramPayloadBytes   = netwire.MaxDatagramBytes - netwire.ReliableOrderedOverhead
	maxEventualStateDatagramPayloadBytes     = netwire.MaxDatagramBytes - netwire.EventualStateOverhead
)

// CertificateHash identifies a certificate by digest for browser WebTransport.
type CertificateHash struct {
	Algorithm string
	Value     []byte
}

func normalizeTransport(t Transport) Transport {
	if t == "" {
		return TransportWebSocket
	}
	return t
}

// validateReliableMessageSize reports an error when one logical reliable
// message exceeds the transport-neutral message cap.
func validateReliableMessageSize(frame []byte) error {
	if len(frame) > maxReliableMessageBytes {
		return fmt.Errorf("golem/net: reliable message size %d exceeds max reliable message %d", len(frame), maxReliableMessageBytes)
	}
	return nil
}

// validateReliableMessageSizes reports the first logical reliable message in a
// batch that exceeds the transport-neutral message cap.
func validateReliableMessageSizes(frames [][]byte) error {
	for i, frame := range frames {
		if err := validateReliableMessageSize(frame); err != nil {
			return fmt.Errorf("frame %d: %w", i, err)
		}
	}
	return nil
}

type reliableChunkEncoder struct {
	encodedLen  func(frame []byte) int
	appendFrame func(dst []byte, frame []byte) []byte
}

var rawReliableChunkEncoder = reliableChunkEncoder{
	encodedLen: func(frame []byte) int { return len(frame) },
	appendFrame: func(dst []byte, frame []byte) []byte {
		return append(dst, frame...)
	},
}

var lengthPrefixedReliableChunkEncoder = reliableChunkEncoder{
	encodedLen:  func(frame []byte) int { return reliableFrameHeaderBytes + len(frame) },
	appendFrame: appendLengthPrefixedReliableFrame,
}

// chunkReliableOrderedDatagramFrames packs wrapped entity-update frames into
// one or more ordered-datagram payloads using the same length-prefixed framing
// as the reliable stream path. Unlike chunkReliableFrames, this enforces the
// ordered-datagram payload ceiling strictly: a single encoded frame may not
// exceed maxReliableOrderedDatagramPayloadBytes.
func chunkReliableOrderedDatagramFrames(frames [][]byte) ([][]byte, error) {
	return chunkFrames(frames, maxReliableOrderedDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false)
}

// UnreliableStateDatagramPayloads packs wrapped entity-update frames into one
// or more raw WebTransport datagram payloads using length-prefixed framing.
// Returned payloads own their bytes; inputs remain unchanged. Every encoded
// frame must fit the payload budget, and an error returns no partial output.
func UnreliableStateDatagramPayloads(frames [][]byte) ([][]byte, error) {
	return chunkFrames(frames, maxUnreliableStateDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false)
}

// UnreliableStateDatagramPayloadBudget returns the payload bytes available
// inside one raw WebTransport state datagram.
func UnreliableStateDatagramPayloadBudget() int {
	return maxUnreliableStateDatagramPayloadBytes
}

// UnreliableStateDatagramFramePayloadLen returns the payload bytes needed to
// carry one wrapped entity-update frame inside a raw state datagram.
func UnreliableStateDatagramFramePayloadLen(frame []byte) int {
	return lengthPrefixedReliableChunkEncoder.encodedLen(frame)
}

// AppendUnreliableStateDatagramFrame appends one length-prefixed wrapped frame
// to a raw state datagram payload under construction.
func AppendUnreliableStateDatagramFrame(dst []byte, frame []byte) []byte {
	return lengthPrefixedReliableChunkEncoder.appendFrame(dst, frame)
}

// EventualStateDatagramPayloads packs wrapped entity-update frames into one or
// more eventual-state datagram payloads using length-prefixed framing.
// Returned payloads own their bytes; inputs remain unchanged. Every encoded
// frame must fit the payload budget, and an error returns no partial output.
func EventualStateDatagramPayloads(frames [][]byte) ([][]byte, error) {
	return chunkFrames(frames, maxEventualStateDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false)
}

// EventualStateDatagramPayloadBudget returns the payload bytes available inside
// one eventual-state datagram after protocol headers.
func EventualStateDatagramPayloadBudget() int {
	return maxEventualStateDatagramPayloadBytes
}

// EventualStateDatagramFramePayloadLen returns the payload bytes needed to
// carry one wrapped entity-update frame inside an eventual-state datagram.
func EventualStateDatagramFramePayloadLen(frame []byte) int {
	return lengthPrefixedReliableChunkEncoder.encodedLen(frame)
}

// AppendEventualStateDatagramFrame appends one length-prefixed wrapped frame to
// an eventual-state datagram payload under construction.
func AppendEventualStateDatagramFrame(dst []byte, frame []byte) []byte {
	return lengthPrefixedReliableChunkEncoder.appendFrame(dst, frame)
}

// CompactStateDatagramFramePayloadLen returns the payload bytes needed to carry
// one compact state record inside a state datagram.
func CompactStateDatagramFramePayloadLen(frame []byte) int {
	return lengthPrefixedReliableChunkEncoder.encodedLen(frame)
}

// AppendCompactStateDatagramFrame appends one length-prefixed compact state
// record to a state datagram payload under construction.
func AppendCompactStateDatagramFrame(dst []byte, frame []byte) []byte {
	return lengthPrefixedReliableChunkEncoder.appendFrame(dst, frame)
}

// ReliableOrderedStateDatagramPayloadBudget returns the payload bytes available
// inside one reliable ordered datagram after protocol headers.
func ReliableOrderedStateDatagramPayloadBudget() int {
	return maxReliableOrderedDatagramPayloadBytes
}

// ReliableStreamWriteChunkCount returns how many separate writes the reliable
// stream lane performs for the given pre-wrapped frames (same rules as
// WebSocket and WebTransport reliable stream batch writers).
// It allocates no payload buffers and returns zero on validation failure.
func ReliableStreamWriteChunkCount(t Transport, frames [][]byte) (int, error) {
	if len(frames) == 0 {
		return 0, nil
	}
	t = normalizeTransport(t)
	switch t {
	case TransportWebSocket:
		return countReliableFrameChunks(frames, maxWebSocketPayloadBytes, rawReliableChunkEncoder)
	case TransportWebTransport:
		return countReliableFrameChunks(frames, maxReliableChunkBytes, lengthPrefixedReliableChunkEncoder)
	default:
		return 0, fmt.Errorf("golem/net: unknown transport %q", t)
	}
}

// ReliableOrderedDatagramPayloadCount returns how many reliable-ordered
// datagram payloads chunkReliableOrderedDatagramFrames would produce.
// It allocates no payload buffers and returns zero on validation failure.
func ReliableOrderedDatagramPayloadCount(frames [][]byte) (int, error) {
	return countFrameChunks(frames, maxReliableOrderedDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false, "ordered datagram")
}

// UnreliableStateDatagramPayloadCount returns how many raw unreliable state
// datagram payloads UnreliableStateDatagramPayloads would produce.
// It allocates no payload buffers and returns zero on validation failure.
func UnreliableStateDatagramPayloadCount(frames [][]byte) (int, error) {
	return countFrameChunks(frames, maxUnreliableStateDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false, "unreliable state datagram")
}

// EventualStateDatagramPayloadCount returns how many eventual-state datagram
// payloads EventualStateDatagramPayloads would produce.
// It allocates no payload buffers and returns zero on validation failure.
func EventualStateDatagramPayloadCount(frames [][]byte) (int, error) {
	return countFrameChunks(frames, maxEventualStateDatagramPayloadBytes, lengthPrefixedReliableChunkEncoder, false, "ordered datagram")
}

// validateWebSocketPayloadSize reports an error when a single WebSocket payload
// exceeds the hard wire cap.
func validateWebSocketPayloadSize(payload []byte) error {
	if len(payload) > maxWebSocketPayloadBytes {
		return fmt.Errorf("golem/net: websocket payload size %d exceeds max websocket payload %d", len(payload), maxWebSocketPayloadBytes)
	}
	return nil
}

// validateDatagramSize reports an error when a datagram exceeds the hard
// WebTransport datagram cap.
func validateDatagramSize(data []byte) error {
	if len(data) > maxWebTransportDatagramBytes {
		return fmt.Errorf("golem/net: datagram size %d exceeds max webtransport datagram %d", len(data), maxWebTransportDatagramBytes)
	}
	return nil
}

// chunkReliableFrames packs logical reliable frames into transport-ready chunks.
func chunkReliableFrames(frames [][]byte, maxChunkBytes int, encoder reliableChunkEncoder) ([][]byte, error) {
	return chunkFrames(frames, maxChunkBytes, encoder, true)
}

// writeReliableFrameChunks writes reliable stream chunks without materializing a
// [][]byte result. The returned scratch buffer may be reused by the caller on
// the next batch after write returns.
func writeReliableFrameChunks(frames [][]byte, maxChunkBytes int, encoder reliableChunkEncoder, scratch []byte, write func([]byte) error) ([]byte, error) {
	if len(frames) == 0 {
		return scratch[:0], nil
	}
	sizer, err := netwire.NewChunkSizer(maxChunkBytes, true)
	if err != nil {
		return scratch[:0], chunkSizeError(err, "ordered datagram")
	}
	buf := scratch[:0]
	for i, frame := range frames {
		flush, err := sizer.Add(len(frame), encoder.encodedLen(frame))
		if err != nil {
			return buf[:0], fmt.Errorf("frame %d: %w", i, chunkSizeError(err, "ordered datagram"))
		}
		if flush {
			if err := write(buf); err != nil {
				return buf[:0], err
			}
			buf = buf[:0]
		}
		buf = encoder.appendFrame(buf, frame)
	}
	if len(buf) > 0 {
		if err := write(buf); err != nil {
			return buf[:0], err
		}
	}
	return buf[:0], nil
}

func countReliableFrameChunks(frames [][]byte, maxChunkBytes int, encoder reliableChunkEncoder) (int, error) {
	return countFrameChunks(frames, maxChunkBytes, encoder, true, "ordered datagram")
}

func countFrameChunks(frames [][]byte, maxChunkBytes int, encoder reliableChunkEncoder, allowOversizedSingle bool, diagnostic string) (int, error) {
	if len(frames) == 0 {
		return 0, nil
	}
	sizer, err := netwire.NewChunkSizer(maxChunkBytes, allowOversizedSingle)
	if err != nil {
		return 0, chunkSizeError(err, diagnostic)
	}
	chunks := 0
	for i, frame := range frames {
		flush, err := sizer.Add(len(frame), encoder.encodedLen(frame))
		if err != nil {
			return 0, fmt.Errorf("frame %d: %w", i, chunkSizeError(err, diagnostic))
		}
		if flush {
			chunks++
		}
	}
	if sizer.PendingBytes() > 0 {
		chunks++
	}
	return chunks, nil
}

func chunkFrames(frames [][]byte, maxChunkBytes int, encoder reliableChunkEncoder, allowOversizedSingle bool) ([][]byte, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	sizer, err := netwire.NewChunkSizer(maxChunkBytes, allowOversizedSingle)
	if err != nil {
		return nil, chunkSizeError(err, "ordered datagram")
	}
	var chunks [][]byte
	var buf []byte
	for i, frame := range frames {
		encodedLen := encoder.encodedLen(frame)
		flush, err := sizer.Add(len(frame), encodedLen)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, chunkSizeError(err, "ordered datagram"))
		}
		if flush {
			chunks = append(chunks, buf)
			buf = nil
		}
		if buf == nil {
			buf = make([]byte, 0, max(maxChunkBytes, encodedLen))
		}
		buf = encoder.appendFrame(buf, frame)
	}
	if len(buf) > 0 {
		chunks = append(chunks, buf)
	}
	return chunks, nil
}

func chunkSizeError(err error, diagnostic string) error {
	size, ok := err.(netwire.ChunkSizeError)
	if !ok {
		return err
	}
	switch size.Kind {
	case netwire.InvalidChunkTarget:
		return fmt.Errorf("golem/net: invalid reliable chunk size %d", size.Size)
	case netwire.LogicalFrameTooLarge:
		return fmt.Errorf("golem/net: reliable message size %d exceeds max reliable message %d", size.Size, size.Limit)
	default:
		return fmt.Errorf("golem/net: encoded frame size %d exceeds max %s payload %d", size.Size, diagnostic, size.Limit)
	}
}

// writeReliableFrame writes one length-prefixed reliable message to w.
func writeReliableFrame(w io.Writer, data []byte) error {
	err := netwire.WriteFrame(w, data)
	if size, ok := err.(netwire.SizeError); ok {
		return fmt.Errorf("golem/net: reliable message size %d exceeds max reliable message %d", size.Size, size.Limit)
	}
	return err
}

// appendLengthPrefixedReliableFrame appends one framed reliable message to dst.
func appendLengthPrefixedReliableFrame(dst, data []byte) []byte {
	return netwire.AppendFrame(dst, data)
}

// readReliableFrame reads one length-prefixed reliable message from r.
func readReliableFrame(r io.Reader) ([]byte, error) {
	data, err := netwire.ReadFrame(r)
	if size, ok := err.(netwire.SizeError); ok {
		return nil, fmt.Errorf("golem/net: reliable frame length %d exceeds max reliable message %d", size.Size, size.Limit)
	}
	return data, err
}
