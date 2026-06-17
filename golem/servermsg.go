package golem

import "golem-engine/golem/pb"

// WrapEntityUpdate wraps serialized EntityUpdate bytes in a ServerMessage
// envelope (proto field 1, length-delimited). The Listener applies this
// transparently to all outgoing entity frames when integrated networking is active.
func WrapEntityUpdate(data []byte) []byte {
	w := &pb.Writer{}
	w.Tag(1, 2) // field 1 = entity_update, wire type 2 = length-delimited
	w.Bytes(data)
	return w.Finish()
}

// WrapWorldUpdate wraps serialized WorldUpdate bytes in a ServerMessage
// envelope (proto field 2, length-delimited). Used by PushWorldData and the
// world snapshot closure — world frames bypass the entity messageWrapper.
func WrapWorldUpdate(data []byte) []byte {
	w := &pb.Writer{}
	w.Tag(2, 2) // field 2 = world_update, wire type 2 = length-delimited
	w.Bytes(data)
	return w.Finish()
}

// WrapServerEvent wraps serialized ServerEvent bytes in a ServerMessage
// envelope (proto field 3, length-delimited). Used by EventBroadcaster methods
// — event frames bypass the entity messageWrapper.
func WrapServerEvent(data []byte) []byte {
	w := &pb.Writer{}
	w.Tag(3, 2) // field 3 = server_event, wire type 2 = length-delimited
	w.Bytes(data)
	return w.Finish()
}
