// Package netwire owns the Go runtimes' wire representation and pure framing
// and receive algorithms. Callers own validation policy, scheduling and locking.
package netwire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MaxReliableMessageBytes         = 256 * 1024
	MaxDatagramBytes                = 1200
	FrameHeaderBytes                = 4
	AckMaskWordCount                = 4
	AckMaskWordBytes                = 4
	AckMaskBytes                    = AckMaskWordCount * AckMaskWordBytes
	PacketHeaderBytes               = 2 + 2 + AckMaskBytes + 1
	LaneHeaderBytes                 = 1
	ReliableMessageIDBytes          = 2
	OrderedSequenceBytes            = 2
	StateTokenBytes                 = 8
	AckWindow                       = AckMaskWordCount * 32
	FlagACKOnly               uint8 = 1
	UnreliableOverhead              = PacketHeaderBytes + LaneHeaderBytes
	ReliableUnorderedOverhead       = UnreliableOverhead + ReliableMessageIDBytes
	ReliableOrderedOverhead         = ReliableUnorderedOverhead + OrderedSequenceBytes
	EventualStateOverhead           = UnreliableOverhead + StateTokenBytes
)

// Lane identifies a protocol datagram lane; raw state datagrams have no lane header.
type Lane = uint8

const (
	LaneUnreliable Lane = iota + 1
	LaneReliableUnordered
	LaneReliableOrdered
	LaneEventualState
)

// AckMask records receipt of the 128 packets preceding an ACK sequence.
type AckMask [AckMaskWordCount]uint32

// ACK is a selective acknowledgement snapshot.
type ACK struct {
	Sequence uint16
	Mask     AckMask
}

// Packet is the transport-independent protocol datagram representation.
type Packet struct {
	Sequence        uint16
	ACK             ACK
	Flags           uint8
	Lane            Lane
	MessageID       uint16
	OrderedSequence uint16
	StateToken      uint64
	Payload         []byte
}

var (
	ErrPacketHeader  = errors.New("datagram packet too small")
	ErrLaneHeader    = errors.New("datagram lane missing")
	ErrMessageID     = errors.New("reliable datagram message id missing")
	ErrOrderedHeader = errors.New("ordered datagram header missing")
	ErrStateToken    = errors.New("state datagram token missing")
)

// UnknownLaneError reports an unrecognized protocol lane.
type UnknownLaneError struct{ Lane Lane }

// Error describes the invalid lane without an endpoint-specific prefix.
func (e UnknownLaneError) Error() string { return fmt.Sprintf("unknown datagram lane %d", e.Lane) }

// PayloadBudget subtracts lane overhead from the caller's physical packet limit.
// A negative budget is intentional when the supplied limit cannot fit a header.
func PayloadBudget(packetLimit int, lane Lane) (int, error) {
	switch lane {
	case LaneUnreliable:
		return packetLimit - UnreliableOverhead, nil
	case LaneReliableUnordered:
		return packetLimit - ReliableUnorderedOverhead, nil
	case LaneReliableOrdered:
		return packetLimit - ReliableOrderedOverhead, nil
	case LaneEventualState:
		return packetLimit - EventualStateOverhead, nil
	default:
		return 0, UnknownLaneError{lane}
	}
}

// EncodePacketInto resets and reuses dst, returning encoded bytes valid until
// that storage is reused. Payload must not overlap dst. Size policy is caller-owned.
func EncodePacketInto(dst []byte, p Packet) ([]byte, error) {
	n := PacketHeaderBytes
	if p.Flags&FlagACKOnly == 0 {
		budget, err := PayloadBudget(0, p.Lane)
		if err != nil {
			return nil, err
		}
		n = -budget + len(p.Payload)
	}
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}
	binary.BigEndian.PutUint16(dst[:2], p.Sequence)
	putACK(dst[2:4+AckMaskBytes], p.ACK)
	dst[PacketHeaderBytes-1] = p.Flags
	if p.Flags&FlagACKOnly != 0 {
		return dst, nil
	}
	offset := PacketHeaderBytes
	dst[offset] = p.Lane
	offset++
	switch p.Lane {
	case LaneReliableUnordered, LaneReliableOrdered:
		binary.BigEndian.PutUint16(dst[offset:offset+2], p.MessageID)
		offset += 2
		if p.Lane == LaneReliableOrdered {
			binary.BigEndian.PutUint16(dst[offset:offset+2], p.OrderedSequence)
			offset += 2
		}
	case LaneEventualState:
		binary.BigEndian.PutUint64(dst[offset:offset+8], p.StateToken)
		offset += 8
	}
	copy(dst[offset:], p.Payload)
	return dst, nil
}

// DecodePacket parses structure only and borrows the payload from data. It
// intentionally ignores unknown flag bits and trailing bytes on ACK-only packets.
func DecodePacket(data []byte) (Packet, error) {
	if len(data) < PacketHeaderBytes {
		return Packet{}, ErrPacketHeader
	}
	p := Packet{Sequence: binary.BigEndian.Uint16(data[:2]), ACK: readACK(data[2 : 4+AckMaskBytes]), Flags: data[PacketHeaderBytes-1]}
	if p.Flags&FlagACKOnly != 0 {
		return p, nil
	}
	offset := PacketHeaderBytes
	if len(data) <= offset {
		return Packet{}, ErrLaneHeader
	}
	p.Lane = data[offset]
	offset++
	switch p.Lane {
	case LaneUnreliable:
	case LaneReliableUnordered:
		if len(data) < offset+2 {
			return Packet{}, ErrMessageID
		}
		p.MessageID = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
	case LaneReliableOrdered:
		if len(data) < offset+4 {
			return Packet{}, ErrOrderedHeader
		}
		p.MessageID = binary.BigEndian.Uint16(data[offset : offset+2])
		p.OrderedSequence = binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
	case LaneEventualState:
		if len(data) < offset+8 {
			return Packet{}, ErrStateToken
		}
		p.StateToken = binary.BigEndian.Uint64(data[offset : offset+8])
		offset += 8
	default:
		return Packet{}, UnknownLaneError{p.Lane}
	}
	p.Payload = data[offset:]
	return p, nil
}

func putACK(dst []byte, ack ACK) {
	binary.BigEndian.PutUint16(dst[:2], ack.Sequence)
	for i, word := range ack.Mask {
		binary.BigEndian.PutUint32(dst[2+i*4:6+i*4], word)
	}
}

func readACK(data []byte) ACK {
	ack := ACK{Sequence: binary.BigEndian.Uint16(data[:2])}
	for i := range ack.Mask {
		ack.Mask[i] = binary.BigEndian.Uint32(data[2+i*4 : 6+i*4])
	}
	return ack
}
