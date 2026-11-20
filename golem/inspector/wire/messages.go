package wire

import (
	"fmt"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/pb"
)

// ProtocolVersion is the inspector protocol version carried by every Envelope.
const ProtocolVersion = 1

// ErrVersion reports an envelope with an unsupported protocol version.
var ErrVersion = fmt.Errorf("wire: unsupported protocol version")

// ObserveState is the agent's cold-observation state.
type ObserveState uint32

// Observation states, matching inspector.proto ObserveState.
const (
	ObserveIdle     ObserveState = 0
	ObserveStarting ObserveState = 1
	ObserveReady    ObserveState = 2
)

// String returns the lowercase state name used by the REST API and logs.
func (s ObserveState) String() string {
	switch s {
	case ObserveIdle:
		return "idle"
	case ObserveStarting:
		return "starting"
	case ObserveReady:
		return "ready"
	}
	return fmt.Sprintf("state(%d)", uint32(s))
}

// Message is implemented by every envelope body type.
type Message interface {
	bodyTag() int
	encodeBody(w *pb.Writer)
}

// Register is the first message an agent sends after connecting.
type Register struct {
	TargetID      string            `json:"targetId"`
	Labels        map[string]string `json:"labels,omitempty"`
	Epoch         uint64            `json:"epoch"`
	EngineVersion string            `json:"engineVersion,omitempty"`
	Schema        *Schema           `json:"schema"`
	TickRate      uint32            `json:"tickRate,omitempty"`
}

// ObserveStatus reports the agent's observation state to the hub.
type ObserveStatus struct {
	State       ObserveState `json:"state"`
	EntityCount uint64       `json:"entityCount"`
	Epoch       uint64       `json:"epoch"`
}

// SnapshotBegin opens a full-world snapshot.
type SnapshotBegin struct {
	TargetID    string `json:"targetId,omitempty"`
	Epoch       uint64 `json:"epoch"`
	Seq         uint64 `json:"seq"`
	Tick        uint64 `json:"tick"`
	EntityCount uint64 `json:"entityCount"`
}

// SnapshotChunk carries part of a snapshot's entities.
type SnapshotChunk struct {
	TargetID string        `json:"targetId,omitempty"`
	Epoch    uint64        `json:"epoch"`
	Seq      uint64        `json:"seq"`
	Entities []EntityState `json:"entities"`
}

// SnapshotEnd closes a snapshot.
type SnapshotEnd struct {
	TargetID string `json:"targetId,omitempty"`
	Epoch    uint64 `json:"epoch"`
	Seq      uint64 `json:"seq"`
}

// Batch carries coalesced live changes after a snapshot.
type Batch struct {
	TargetID string        `json:"targetId,omitempty"`
	Epoch    uint64        `json:"epoch"`
	Seq      uint64        `json:"seq"`
	Tick     uint64        `json:"tick"`
	Spawns   []EntityState `json:"spawns,omitempty"`
	Deltas   []EntityDelta `json:"deltas,omitempty"`
	Removals []Removal     `json:"removals,omitempty"`
}

// EntityState is one entity's full state; State is the game's XState message.
type EntityState struct {
	ID      int64  `json:"id"`
	TypeTag uint32 `json:"typeTag"`
	State   []byte `json:"state"`
}

// EntityDelta is one entity's change; Delta is the game's XDelta message and
// Mask the dirty bits it covers.
type EntityDelta struct {
	ID       int64  `json:"id"`
	TypeTag  uint32 `json:"typeTag"`
	Revision uint64 `json:"revision"`
	Mask     uint64 `json:"mask"`
	Delta    []byte `json:"delta"`
}

// Removal reports an entity that left the world.
type Removal struct {
	ID       int64  `json:"id"`
	Revision uint64 `json:"revision"`
}

// Ping is a heartbeat; the hub answers with Pong echoing ClientTime.
type Ping struct {
	ClientTime int64 `json:"clientTime"`
}

// Pong answers Ping.
type Pong struct {
	ClientTime int64 `json:"clientTime"`
}

// ObserveStart asks an agent to begin streaming.
type ObserveStart struct{}

// ObserveStop asks an agent to stop streaming.
type ObserveStop struct{}

// Error reports a protocol or request error.
type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
	TargetID string `json:"targetId,omitempty"`
}

// Subscribe selects the target a browser connection views.
type Subscribe struct {
	TargetID string `json:"targetId"`
}

// Resync asks the hub for a fresh snapshot of the subscribed target.
type Resync struct {
	TargetID string `json:"targetId"`
}

// TargetSchema sends a target's schema to a browser before its snapshot.
type TargetSchema struct {
	TargetID string  `json:"targetId"`
	Schema   *Schema `json:"schema"`
}

// WorldStatus summarizes a target for a browser.
type WorldStatus struct {
	TargetID       string       `json:"targetId"`
	Epoch          uint64       `json:"epoch"`
	EntityCount    uint64       `json:"entityCount"`
	Ready          bool         `json:"ready"`
	AgentConnected bool         `json:"agentConnected"`
	ObserveState   ObserveState `json:"observeState"`
	Tick           uint64       `json:"tick"`
}

// ResyncRequired tells a browser to discard its state for a target.
type ResyncRequired struct {
	TargetID string `json:"targetId"`
	Epoch    uint64 `json:"epoch"`
	Reason   string `json:"reason"`
}

// Envelope body tags, matching inspector.proto.
const (
	tagRegister       = 10
	tagObserveStatus  = 11
	tagSnapshotBegin  = 12
	tagSnapshotChunk  = 13
	tagSnapshotEnd    = 14
	tagBatch          = 15
	tagPing           = 16
	tagPong           = 17
	tagObserveStart   = 20
	tagObserveStop    = 21
	tagError          = 22
	tagSubscribe      = 30
	tagResync         = 31
	tagTargetSchema   = 40
	tagWorldStatus    = 41
	tagResyncRequired = 42
)

func (*Register) bodyTag() int       { return tagRegister }
func (*ObserveStatus) bodyTag() int  { return tagObserveStatus }
func (*SnapshotBegin) bodyTag() int  { return tagSnapshotBegin }
func (*SnapshotChunk) bodyTag() int  { return tagSnapshotChunk }
func (*SnapshotEnd) bodyTag() int    { return tagSnapshotEnd }
func (*Batch) bodyTag() int          { return tagBatch }
func (*Ping) bodyTag() int           { return tagPing }
func (*Pong) bodyTag() int           { return tagPong }
func (*ObserveStart) bodyTag() int   { return tagObserveStart }
func (*ObserveStop) bodyTag() int    { return tagObserveStop }
func (*Error) bodyTag() int          { return tagError }
func (*Subscribe) bodyTag() int      { return tagSubscribe }
func (*Resync) bodyTag() int         { return tagResync }
func (*TargetSchema) bodyTag() int   { return tagTargetSchema }
func (*WorldStatus) bodyTag() int    { return tagWorldStatus }
func (*ResyncRequired) bodyTag() int { return tagResyncRequired }

// Encode wraps msg in a version-1 Envelope.
func Encode(msg Message) []byte {
	body := &pb.Writer{}
	msg.encodeBody(body)
	w := &pb.Writer{}
	w.Tag(1, wireVarint).Uint32(ProtocolVersion)
	putMessage(w, msg.bodyTag(), body.Finish())
	return w.Finish()
}

// Decode parses an Envelope and returns its body. It returns ErrVersion for
// other protocol versions and ErrMalformed for invalid bytes or a missing body.
// Byte fields in the result alias buf.
func Decode(buf []byte) (Message, error) {
	r := newReader(buf)
	var version uint32
	var msg Message
	for !r.done() {
		f, wt := r.tag()
		if f == 1 {
			version = r.uint32Field(f, wt)
			continue
		}
		if wt != wireBytes {
			r.skip(wt)
			continue
		}
		body := r.bytes()
		if r.err != nil {
			break
		}
		if m := decodeBody(int(f), body, r); m != nil {
			msg = m
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("%w: empty envelope", ErrMalformed)
	}
	if version != ProtocolVersion {
		return nil, fmt.Errorf("%w %d", ErrVersion, version)
	}
	if msg == nil {
		return nil, fmt.Errorf("%w: envelope has no known body", ErrMalformed)
	}
	return msg, nil
}

func decodeBody(tag int, buf []byte, parent *reader) Message {
	r := newReader(buf)
	var m Message
	switch tag {
	case tagRegister:
		m = decodeRegister(r)
	case tagObserveStatus:
		v := &ObserveStatus{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.State = ObserveState(r.uint32Field(f, wt))
			case 2:
				v.EntityCount = r.uint64Field(f, wt)
			case 3:
				v.Epoch = r.uint64Field(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagSnapshotBegin:
		v := &SnapshotBegin{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				v.Epoch = r.uint64Field(f, wt)
			case 3:
				v.Seq = r.uint64Field(f, wt)
			case 4:
				v.Tick = r.uint64Field(f, wt)
			case 5:
				v.EntityCount = r.uint64Field(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagSnapshotChunk:
		v := &SnapshotChunk{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				v.Epoch = r.uint64Field(f, wt)
			case 3:
				v.Seq = r.uint64Field(f, wt)
			case 4:
				v.Entities = append(v.Entities, decodeEntityState(r, r.bytesField(f, wt)))
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagSnapshotEnd:
		v := &SnapshotEnd{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				v.Epoch = r.uint64Field(f, wt)
			case 3:
				v.Seq = r.uint64Field(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagBatch:
		m = decodeBatch(r)
	case tagPing:
		v := &Ping{}
		for !r.done() {
			f, wt := r.tag()
			if f == 1 {
				v.ClientTime = r.int64Field(f, wt)
			} else {
				r.skip(wt)
			}
		}
		m = v
	case tagPong:
		v := &Pong{}
		for !r.done() {
			f, wt := r.tag()
			if f == 1 {
				v.ClientTime = r.int64Field(f, wt)
			} else {
				r.skip(wt)
			}
		}
		m = v
	case tagObserveStart:
		m = &ObserveStart{}
	case tagObserveStop:
		m = &ObserveStop{}
	case tagError:
		v := &Error{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.Code = r.stringField(f, wt)
			case 2:
				v.Message = r.stringField(f, wt)
			case 3:
				v.TargetID = r.stringField(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagSubscribe:
		v := &Subscribe{}
		for !r.done() {
			f, wt := r.tag()
			if f == 1 {
				v.TargetID = r.stringField(f, wt)
			} else {
				r.skip(wt)
			}
		}
		m = v
	case tagResync:
		v := &Resync{}
		for !r.done() {
			f, wt := r.tag()
			if f == 1 {
				v.TargetID = r.stringField(f, wt)
			} else {
				r.skip(wt)
			}
		}
		m = v
	case tagTargetSchema:
		v := &TargetSchema{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				sub := newReader(r.bytesField(f, wt))
				v.Schema = decodeSchema(sub)
				r.adopt(sub)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagWorldStatus:
		v := &WorldStatus{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				v.Epoch = r.uint64Field(f, wt)
			case 3:
				v.EntityCount = r.uint64Field(f, wt)
			case 4:
				v.Ready = r.boolField(f, wt)
			case 5:
				v.AgentConnected = r.boolField(f, wt)
			case 6:
				v.ObserveState = ObserveState(r.uint32Field(f, wt))
			case 7:
				v.Tick = r.uint64Field(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	case tagResyncRequired:
		v := &ResyncRequired{}
		for !r.done() {
			f, wt := r.tag()
			switch f {
			case 1:
				v.TargetID = r.stringField(f, wt)
			case 2:
				v.Epoch = r.uint64Field(f, wt)
			case 3:
				v.Reason = r.stringField(f, wt)
			default:
				r.skip(wt)
			}
		}
		m = v
	default:
		return nil // unknown bodies from newer peers are ignored
	}
	parent.adopt(r)
	return m
}

func decodeRegister(r *reader) *Register {
	v := &Register{}
	for !r.done() {
		f, wt := r.tag()
		switch f {
		case 1:
			v.TargetID = r.stringField(f, wt)
		case 2:
			sub := newReader(r.bytesField(f, wt))
			var k, val string
			for !sub.done() {
				ef, ewt := sub.tag()
				switch ef {
				case 1:
					k = sub.stringField(ef, ewt)
				case 2:
					val = sub.stringField(ef, ewt)
				default:
					sub.skip(ewt)
				}
			}
			r.adopt(sub)
			if v.Labels == nil {
				v.Labels = map[string]string{}
			}
			v.Labels[k] = val
		case 3:
			v.Epoch = r.uint64Field(f, wt)
		case 4:
			v.EngineVersion = r.stringField(f, wt)
		case 5:
			sub := newReader(r.bytesField(f, wt))
			v.Schema = decodeSchema(sub)
			r.adopt(sub)
		case 6:
			v.TickRate = r.uint32Field(f, wt)
		default:
			r.skip(wt)
		}
	}
	return v
}

func decodeBatch(r *reader) *Batch {
	v := &Batch{}
	for !r.done() {
		f, wt := r.tag()
		switch f {
		case 1:
			v.TargetID = r.stringField(f, wt)
		case 2:
			v.Epoch = r.uint64Field(f, wt)
		case 3:
			v.Seq = r.uint64Field(f, wt)
		case 4:
			v.Tick = r.uint64Field(f, wt)
		case 5:
			v.Spawns = append(v.Spawns, decodeEntityState(r, r.bytesField(f, wt)))
		case 6:
			v.Deltas = append(v.Deltas, decodeEntityDelta(r, r.bytesField(f, wt)))
		case 7:
			sub := newReader(r.bytesField(f, wt))
			rm := Removal{}
			for !sub.done() {
				ef, ewt := sub.tag()
				switch ef {
				case 1:
					rm.ID = sub.int64Field(ef, ewt)
				case 2:
					rm.Revision = sub.uint64Field(ef, ewt)
				default:
					sub.skip(ewt)
				}
			}
			r.adopt(sub)
			v.Removals = append(v.Removals, rm)
		default:
			r.skip(wt)
		}
	}
	return v
}

func decodeEntityState(parent *reader, buf []byte) EntityState {
	r := newReader(buf)
	e := EntityState{}
	for !r.done() {
		f, wt := r.tag()
		switch f {
		case 1:
			e.ID = r.int64Field(f, wt)
		case 2:
			e.TypeTag = r.uint32Field(f, wt)
		case 3:
			e.State = r.bytesField(f, wt)
		default:
			r.skip(wt)
		}
	}
	parent.adopt(r)
	return e
}

func decodeEntityDelta(parent *reader, buf []byte) EntityDelta {
	r := newReader(buf)
	e := EntityDelta{}
	for !r.done() {
		f, wt := r.tag()
		switch f {
		case 1:
			e.ID = r.int64Field(f, wt)
		case 2:
			e.TypeTag = r.uint32Field(f, wt)
		case 3:
			e.Revision = r.uint64Field(f, wt)
		case 4:
			e.Mask = r.uint64Field(f, wt)
		case 5:
			e.Delta = r.bytesField(f, wt)
		default:
			r.skip(wt)
		}
	}
	parent.adopt(r)
	return e
}

func (m *Register) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	keys := make([]string, 0, len(m.Labels))
	for k := range m.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic bytes for golden fixtures
	for _, k := range keys {
		e := &pb.Writer{}
		putString(e, 1, k)
		putString(e, 2, m.Labels[k])
		putMessage(w, 2, e.Finish())
	}
	putUint(w, 3, m.Epoch)
	putString(w, 4, m.EngineVersion)
	if m.Schema != nil {
		putMessage(w, 5, m.Schema.Encode())
	}
	putUint(w, 6, uint64(m.TickRate))
}

func (m *ObserveStatus) encodeBody(w *pb.Writer) {
	putUint(w, 1, uint64(m.State))
	putUint(w, 2, m.EntityCount)
	putUint(w, 3, m.Epoch)
}

func (m *SnapshotBegin) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putUint(w, 3, m.Seq)
	putUint(w, 4, m.Tick)
	putUint(w, 5, m.EntityCount)
}

func (m *SnapshotChunk) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putUint(w, 3, m.Seq)
	for i := range m.Entities {
		putMessage(w, 4, m.Entities[i].encode())
	}
}

func (m *SnapshotEnd) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putUint(w, 3, m.Seq)
}

func (m *Batch) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putUint(w, 3, m.Seq)
	putUint(w, 4, m.Tick)
	for i := range m.Spawns {
		putMessage(w, 5, m.Spawns[i].encode())
	}
	for i := range m.Deltas {
		putMessage(w, 6, m.Deltas[i].encode())
	}
	for i := range m.Removals {
		e := &pb.Writer{}
		putInt(e, 1, m.Removals[i].ID)
		putUint(e, 2, m.Removals[i].Revision)
		putMessage(w, 7, e.Finish())
	}
}

func (e *EntityState) encode() []byte {
	w := &pb.Writer{}
	putInt(w, 1, e.ID)
	putUint(w, 2, uint64(e.TypeTag))
	putBytes(w, 3, e.State)
	return w.Finish()
}

// EncodedSize returns the encoded size of e inside a Batch or SnapshotChunk,
// including its field key and length prefix; used to split frames.
func (e *EntityState) EncodedSize() int { return len(e.State) + 32 }

func (e *EntityDelta) encode() []byte {
	w := &pb.Writer{}
	putInt(w, 1, e.ID)
	putUint(w, 2, uint64(e.TypeTag))
	putUint(w, 3, e.Revision)
	putUint(w, 4, e.Mask)
	putBytes(w, 5, e.Delta)
	return w.Finish()
}

// EncodedSize returns an upper bound for e's encoded size inside a Batch.
func (e *EntityDelta) EncodedSize() int { return len(e.Delta) + 48 }

func (m *Ping) encodeBody(w *pb.Writer) { putInt(w, 1, m.ClientTime) }

func (m *Pong) encodeBody(w *pb.Writer) { putInt(w, 1, m.ClientTime) }

func (*ObserveStart) encodeBody(*pb.Writer) {}

func (*ObserveStop) encodeBody(*pb.Writer) {}

func (m *Error) encodeBody(w *pb.Writer) {
	putString(w, 1, m.Code)
	putString(w, 2, m.Message)
	putString(w, 3, m.TargetID)
}

func (m *Subscribe) encodeBody(w *pb.Writer) { putString(w, 1, m.TargetID) }

func (m *Resync) encodeBody(w *pb.Writer) { putString(w, 1, m.TargetID) }

func (m *TargetSchema) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	if m.Schema != nil {
		putMessage(w, 2, m.Schema.Encode())
	}
}

func (m *WorldStatus) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putUint(w, 3, m.EntityCount)
	putBool(w, 4, m.Ready)
	putBool(w, 5, m.AgentConnected)
	putUint(w, 6, uint64(m.ObserveState))
	putUint(w, 7, m.Tick)
}

func (m *ResyncRequired) encodeBody(w *pb.Writer) {
	putString(w, 1, m.TargetID)
	putUint(w, 2, m.Epoch)
	putString(w, 3, m.Reason)
}

// EncodeBatchBody encodes m without its Seq field so a hub can share one body
// across connections and add a per-connection sequence with EncodeBatchFrame.
func EncodeBatchBody(m *Batch) []byte {
	w := &pb.Writer{}
	shared := *m
	shared.Seq = 0
	shared.encodeBody(w)
	return w.Finish()
}

// EncodeBatchFrame wraps a body from EncodeBatchBody in an Envelope with seq.
// Protobuf merges concatenated fields, so the prefixed seq field is
// equivalent to encoding the Batch with Seq set.
func EncodeBatchFrame(body []byte, seq uint64) []byte {
	head := &pb.Writer{}
	putUint(head, 3, seq)
	h := head.Finish()
	w := &pb.Writer{}
	w.Tag(1, wireVarint).Uint32(ProtocolVersion)
	w.Tag(tagBatch, wireBytes).Uint64(uint64(len(h) + len(body)))
	w.Raw(h)
	w.Raw(body)
	return w.Finish()
}
