package golemclient

import (
	"errors"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// SpaceID identifies the server space whose entities a session currently
// receives. Space 0 is the default space of single-space games.
type SpaceID uint32

// Hold-back limits for compact datagram batches that arrive while a space
// transition is open. Built-in datagrams are at most 1200 bytes, so the batch
// count normally binds first.
const (
	maxHeldSpaceTransitionBatches = 1024
	maxHeldSpaceTransitionBytes   = 4 << 20
)

// ErrSpaceTransitionOverflow reports that more compact datagram batches arrived
// during one open space transition than the client holds back. The client
// closes the connection instead of dropping state.
var ErrSpaceTransitionOverflow = errors.New("golem-go-client: datagram batches held during a space transition exceed the limit")

// spaceTransitionState is one connection's open transition. Only the inbound
// dispatcher goroutine touches it, under GameClient.dispatchMu.
type spaceTransitionState struct {
	open       bool
	transition uint64
	held       [][]byte
	heldBytes  int
}

// Space returns the space announced by the server for the current connection.
// It is 0 before the first SpaceChanged and resets to 0 on connect and
// disconnect without an OnSpaceChanged notification.
func (c *GameClient) Space() SpaceID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.space
}

// OnSpaceChanged registers a callback fired on the inbound dispatch goroutine
// when the server moves this session to another space. It runs before any
// entity, world or event frame that follows the SpaceChanged message, while
// the generated managers are not being updated concurrently.
func (c *GameClient) OnSpaceChanged(fn func(oldSpace, newSpace SpaceID)) {
	c.mu.Lock()
	c.onSpaceChanged = fn
	c.mu.Unlock()
}

// applySpaceChanged opens (or supersedes) a transition and notifies the
// application before the frames that follow it are applied.
func (c *GameClient) applySpaceChanged(d *inboundDispatcher, space SpaceID, transition uint64) {
	d.space.open = true
	d.space.transition = transition
	c.mu.Lock()
	if c.inbound != d {
		c.mu.Unlock()
		return
	}
	old := c.space
	c.space = space
	fn := c.onSpaceChanged
	c.mu.Unlock()
	if fn != nil && old != space {
		fn(old, space)
	}
}

// applySpaceSettled closes the matching open transition, applies the batches
// held back during it in arrival order and acknowledges it with SpaceAck. The
// generated managers' revision fence discards any held state the stream has
// already superseded.
func (c *GameClient) applySpaceSettled(d *inboundDispatcher, transition uint64) {
	if !d.space.open || d.space.transition != transition {
		return
	}
	held := d.space.held
	d.space = spaceTransitionState{}
	for _, batch := range held {
		if d.isDone() {
			return
		}
		c.handleCompactStateBatch(batch)
	}
	c.mu.Lock()
	channel := c.channel
	current := c.inbound == d
	c.mu.Unlock()
	if !current || channel == nil {
		return
	}
	if err := channel.Send(netwire.AppendSpaceAckControl(nil, transition)); err != nil {
		c.setLastErr(sanitizeURLError(err))
	}
}

// holdCompactStateBatch keeps a compact batch until the open transition
// settles, failing the connection when the hold-back limit is exceeded.
func (c *GameClient) holdCompactStateBatch(d *inboundDispatcher, data []byte) {
	if len(d.space.held) >= maxHeldSpaceTransitionBatches || d.space.heldBytes+len(data) > maxHeldSpaceTransitionBytes {
		c.failConnection(d, ErrSpaceTransitionOverflow)
		return
	}
	d.space.held = append(d.space.held, data)
	d.space.heldBytes += len(data)
}

// applyWorldCleared handles a WorldCleared message. World managers do not
// expose per-type clearing yet, so it is decoded and otherwise ignored.
func (c *GameClient) applyWorldCleared(string) {}
