package net

import (
	"errors"
	"time"
)

const (
	datagramAckMaskWordCount             = 4
	datagramAckMaskWordBytes             = 4
	datagramAckMaskBytes                 = datagramAckMaskWordCount * datagramAckMaskWordBytes
	datagramPacketHeaderBytes            = 2 + 2 + datagramAckMaskBytes + 1
	datagramLaneHeaderBytes              = 1
	datagramReliableMessageIDBytes       = 2
	datagramReliableOrderedSequenceBytes = 2
	datagramEventualStateTokenBytes      = 8
	datagramPacketAckWindow              = datagramAckMaskWordCount * 32
	datagramAckCoalesceDelay             = 1 * time.Millisecond
	datagramSchedulerInterval            = 2 * time.Millisecond
	datagramReliableRetryBaseDelay       = 75 * time.Millisecond
	datagramReliableRetryMaxDelay        = 400 * time.Millisecond
	datagramReliableRetryLimit           = 8
	datagramReliableMessageTTL           = 3 * time.Second
	datagramReliableOrderedGapTimeout    = 3 * time.Second
	datagramEventualStateTTL             = 3 * time.Second
	datagramEventualStatePendingLimit    = datagramPacketAckWindow * 2
	maxDatagramDrainSendsPerWake         = 512
	maxDatagramDrainDuration             = 8 * time.Millisecond
	datagramFreshReliableSendReserve     = 64
	datagramResendBudgetPerDrain         = 128
)

const (
	datagramFlagAckOnly uint8 = 1 << iota
)

type datagramLane uint8

const (
	datagramLaneUnreliable datagramLane = iota + 1
	datagramLaneReliableUnordered
	datagramLaneReliableOrdered
	datagramLaneEventualState
)

var (
	errDatagramPacketTooSmall       = errors.New("golem/net: datagram packet too small")
	errDatagramUnknownLane          = errors.New("golem/net: unknown datagram lane")
	errReliableDatagramStalled      = errors.New("golem/net: reliable datagram delivery stalled")
	errReliableOrderedDatagramGap   = errors.New("golem/net: reliable ordered datagram gap expired")
	errEventualStateDatagramStalled = errors.New("golem/net: eventual state datagram delivery stalled")
)

type ackMask128 [datagramAckMaskWordCount]uint32

// setBit marks one zero-based ACK bit inside the selective-ACK window.
func (m *ackMask128) setBit(bit int) {
	if bit < 0 || bit >= datagramPacketAckWindow {
		return
	}
	word := bit / 32
	shift := uint(bit % 32)
	m[word] |= uint32(1) << shift
}

// hasBit reports whether one zero-based ACK bit is set.
func (m ackMask128) hasBit(bit int) bool {
	if bit < 0 || bit >= datagramPacketAckWindow {
		return false
	}
	word := bit / 32
	shift := uint(bit % 32)
	return m[word]&(uint32(1)<<shift) != 0
}

// shiftLeft moves the selective-ACK window toward older packets.
func (m *ackMask128) shiftLeft(bits int) {
	if bits <= 0 {
		return
	}
	if bits >= datagramPacketAckWindow {
		*m = ackMask128{}
		return
	}
	wordShift := bits / 32
	bitShift := uint(bits % 32)
	var shifted ackMask128
	for i := len(shifted) - 1; i >= 0; i-- {
		src := i - wordShift
		if src < 0 {
			continue
		}
		shifted[i] = m[src] << bitShift
		if bitShift > 0 && src > 0 {
			shifted[i] |= m[src-1] >> (32 - bitShift)
		}
	}
	*m = shifted
}

type datagramPacket struct {
	packetSeq  uint16
	ackSeq     uint16
	ackMask    ackMask128
	flags      uint8
	lane       datagramLane
	messageID  uint16
	orderedSeq uint16
	stateToken uint64
	payload    []byte
}

type reliableDatagramMessage struct {
	lane       datagramLane
	messageID  uint16
	orderedSeq uint16
	payload    []byte
	queuedAt   time.Time
	lastSentAt time.Time
	nextSendAt time.Time
	lastPacket uint16
	attempts   int
	inFlight   bool
}

type datagramDelivery struct {
	lane datagramLane
	data []byte
}

// EventualStateDelivery reports whether a state datagram token was observed as
// delivered or lost by the peer's selective ACK stream.
type EventualStateDelivery struct {
	Token     uint64
	Delivered bool
}

type eventualStateDatagram struct {
	token    uint64
	payload  []byte
	queuedAt time.Time
}

type pendingEventualStateDatagram struct {
	token  uint64
	sentAt time.Time
}

type sequenceWindow struct {
	init   bool
	latest uint16
	mask   ackMask128
}

type orderedReceiveState struct {
	nextSeq  uint16
	pending  map[uint16][]byte
	gapSince time.Time
}

// datagramProtocolState tracks protocol headers, resend state, and ordered delivery.
type datagramProtocolState struct {
	nextPacketSeq  uint16
	nextMessageID  uint16
	nextOrderedSeq uint16
	recvPackets    sequenceWindow
	peerPackets    sequenceWindow
	// recvReliableMsgs deduplicates the shared reliable message ID space across
	// both reliable lanes. Ordered and unordered datagrams intentionally consume
	// IDs from the same sequence so the dedupe window covers the combined stream.
	recvReliableMsgs sequenceWindow
	orderedRecv      orderedReceiveState
	ackDirty         bool
	ackDueAt         time.Time

	pendingOrdered      []*reliableDatagramMessage
	pendingUnordered    []*reliableDatagramMessage
	pendingEventual     []eventualStateDatagram
	pendingEventualHead int
	inFlightEventual    map[uint16]pendingEventualStateDatagram
	eventualExpiry      time.Time
	eventualFeedback    []EventualStateDelivery
	inboundPackets      uint64
	inboundAckOnly      uint64
}

func newDatagramProtocolState() *datagramProtocolState {
	return &datagramProtocolState{
		pendingOrdered:   make([]*reliableDatagramMessage, 0, sendBufSize),
		pendingUnordered: make([]*reliableDatagramMessage, 0, sendBufSize),
		pendingEventual:  make([]eventualStateDatagram, 0, sendBufSize),
		inFlightEventual: make(map[uint16]pendingEventualStateDatagram),
	}
}
