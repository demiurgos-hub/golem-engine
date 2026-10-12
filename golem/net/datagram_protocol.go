package net

import (
	"errors"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

const (
	datagramAckMaskWordCount             = netwire.AckMaskWordCount
	datagramAckMaskWordBytes             = netwire.AckMaskWordBytes
	datagramAckMaskBytes                 = netwire.AckMaskBytes
	datagramPacketHeaderBytes            = netwire.PacketHeaderBytes
	datagramLaneHeaderBytes              = netwire.LaneHeaderBytes
	datagramReliableMessageIDBytes       = netwire.ReliableMessageIDBytes
	datagramReliableOrderedSequenceBytes = netwire.OrderedSequenceBytes
	datagramEventualStateTokenBytes      = netwire.StateTokenBytes
	datagramPacketAckWindow              = netwire.AckWindow
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
	datagramFlagAckOnly = netwire.FlagACKOnly
)

type datagramLane = netwire.Lane

const (
	datagramLaneUnreliable        = netwire.LaneUnreliable
	datagramLaneReliableUnordered = netwire.LaneReliableUnordered
	datagramLaneReliableOrdered   = netwire.LaneReliableOrdered
	datagramLaneEventualState     = netwire.LaneEventualState
)

var (
	errDatagramPacketTooSmall       = errors.New("golem/net: datagram packet too small")
	errDatagramUnknownLane          = errors.New("golem/net: unknown datagram lane")
	errReliableDatagramStalled      = errors.New("golem/net: reliable datagram delivery stalled")
	errReliableOrderedDatagramGap   = errors.New("golem/net: reliable ordered datagram gap expired")
	errEventualStateDatagramStalled = errors.New("golem/net: eventual state datagram delivery stalled")
)

type ackMask128 = netwire.AckMask
type datagramACK = netwire.ACK
type datagramPacket = netwire.Packet

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

type sequenceWindow = netwire.SequenceWindow

// datagramProtocolState tracks protocol headers, resend state, and ordered delivery.
type datagramProtocolState struct {
	nextPacketSeq  uint16
	nextMessageID  uint16
	nextOrderedSeq uint16
	recvPackets    sequenceWindow
	// recvReliableMsgs deduplicates unordered messages. Ordered delivery uses
	// its own sequence, although both send lanes share the message ID space.
	recvReliableMsgs sequenceWindow
	orderedRecv      netwire.OrderedBuffer
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
