package golemclient

import "github.com/demiurgos-hub/golem-engine/internal/netwire"

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
	datagramLaneUnreliable               = netwire.LaneUnreliable
	datagramLaneReliableUnordered        = netwire.LaneReliableUnordered
	datagramLaneReliableOrdered          = netwire.LaneReliableOrdered
	datagramLaneEventualState            = netwire.LaneEventualState
	datagramFlagAckOnly                  = netwire.FlagACKOnly
)

type datagramPacket = netwire.Packet
type datagramACK = netwire.ACK
type ackMask128 = netwire.AckMask

const clientReliableAckControlFrame = netwire.ACKControlPrefix
