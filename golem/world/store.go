package world

import (
	"sort"
	"sync"
)

// Data is the interface that generated world data types implement.
// Each type represents a single category of static, server-authoritative
// world information (e.g. zone metadata, map parameters).
type Data interface {
	WorldName() string
	MarshalUpdate() ([]byte, error) // returns WorldUpdate oneof bytes
}

// Store is a thread-safe container for world data values. Generated world
// data types are plain structs with exported fields; Store owns the lock.
// Do not mutate a Data value after passing it to Set — create a new one.
type Store struct {
	mu    sync.RWMutex
	items map[string]Data
}

// NewStore creates an empty world data store.
func NewStore() *Store {
	return &Store{items: make(map[string]Data)}
}

// Set stores a world data value, keyed by its WorldName.
func (s *Store) Set(d Data) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[d.WorldName()] = d
}

// Get returns the stored world data for the given name, or nil if not set.
func (s *Store) Get(name string) Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.items[name]
}

// MarshalAll serializes every stored world data value and returns the results
// in sorted name order for deterministic client snapshots.
func (s *Store) MarshalAll() ([][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.items) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(s.items))
	for name := range s.items {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([][]byte, 0, len(names))
	for _, name := range names {
		data, err := s.items[name].MarshalUpdate()
		if err != nil {
			return nil, err
		}
		result = append(result, data)
	}
	return result, nil
}
