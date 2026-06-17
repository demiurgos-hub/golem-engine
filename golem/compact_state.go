package golem

import (
	"fmt"

	"golem-engine/golem/registry"
)

// compactDeltaForMask returns a compact record for the current values in mask.
func (s *Server) compactDeltaForMask(entityID int64, mask uint64) ([]byte, error) {
	if mask == 0 {
		return nil, nil
	}
	e, ok := s.reg.Get(entityID)
	if !ok {
		return nil, nil
	}
	d, ok := e.(registry.CompactReplicationDeltaEntity)
	if !ok {
		return nil, nil
	}
	data, err := d.MarshalCompactDeltaMask(mask)
	if err != nil {
		return nil, fmt.Errorf("compact delta for entity %d mask %d: %w", entityID, mask, err)
	}
	return data, nil
}
