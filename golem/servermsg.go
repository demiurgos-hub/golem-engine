package golem

import "github.com/demiurgos-hub/golem-engine/golem/pb"

const (
	serverMessageEntityField       = 1
	serverMessageWorldField        = 2
	serverMessageEventField        = 3
	serverMessageSpaceChangedField = 4
	serverMessageSpaceSettledField = 5
	serverMessageWorldClearedField = 6
)

func wrapServerMessage(field int, data []byte) []byte {
	w := &pb.Writer{}
	w.Tag(field, 2)
	w.Bytes(data)
	return w.Finish()
}

// WrapEntityUpdate wraps serialized EntityUpdate bytes in a ServerMessage
// envelope (proto field 1, length-delimited). The Listener applies this
// transparently to all outgoing entity frames when integrated networking is active.
func WrapEntityUpdate(data []byte) []byte {
	return wrapServerMessage(serverMessageEntityField, data)
}

// WrapWorldUpdate wraps serialized WorldUpdate bytes in a ServerMessage
// envelope (proto field 2, length-delimited). Used by PushWorldData,
// SendWorldData / SendStoredWorldData, and the world snapshot closure —
// world frames bypass the entity messageWrapper.
func WrapWorldUpdate(data []byte) []byte {
	return wrapServerMessage(serverMessageWorldField, data)
}

// WrapServerEvent wraps serialized ServerEvent bytes in a ServerMessage
// envelope (proto field 3, length-delimited). Used by EventBroadcaster methods
// — event frames bypass the entity messageWrapper.
func WrapServerEvent(data []byte) []byte {
	return wrapServerMessage(serverMessageEventField, data)
}

// WrapSpaceChanged returns a ServerMessage envelope carrying
// SpaceChanged{space = 1, transition = 2} (proto field 4). Replication sends it
// when a session's space changes; the entity frames that follow on the stream
// belong to the new space until the matching SpaceSettled.
func WrapSpaceChanged(space SpaceID, transition uint64) []byte {
	w := &pb.Writer{}
	if space != 0 {
		w.Tag(1, 0).Uint32(uint32(space))
	}
	if transition != 0 {
		w.Tag(2, 0).Uint64(transition)
	}
	return wrapServerMessage(serverMessageSpaceChangedField, w.Finish())
}

// WrapSpaceSettled returns a ServerMessage envelope carrying
// SpaceSettled{transition = 1} (proto field 5), which ends a space transition.
// Clients acknowledge it with the SpaceAck control frame.
func WrapSpaceSettled(transition uint64) []byte {
	w := &pb.Writer{}
	if transition != 0 {
		w.Tag(1, 0).Uint64(transition)
	}
	return wrapServerMessage(serverMessageSpaceSettledField, w.Finish())
}

// WrapWorldCleared returns a ServerMessage envelope carrying
// WorldCleared{name = 1} (proto field 6), telling a client to drop one world
// data type.
func WrapWorldCleared(name string) []byte {
	w := &pb.Writer{}
	if name != "" {
		w.Tag(1, 2).String(name)
	}
	return wrapServerMessage(serverMessageWorldClearedField, w.Finish())
}
