package golem

import "github.com/demiurgos-hub/golem-engine/golem/pb"

const (
	serverMessageEntityField = 1
	serverMessageWorldField  = 2
	serverMessageEventField  = 3
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
