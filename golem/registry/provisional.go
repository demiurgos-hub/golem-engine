package registry

import (
	"fmt"
	"reflect"
)

// AddProvisional reserves an identity for setup without publishing the entity.
// Get and Owner can resolve it, but enumeration, ticking, snapshots and lifecycle
// output exclude it until CommitProvisional. An optional owner matches AddOwned.
func (r *Registry) AddProvisional(e Entity, owner ...int64) error {
	if len(owner) > 1 {
		return fmt.Errorf("registry: expected at most one provisional owner")
	}
	if nilEntity(e) || e.EntityID() <= 0 {
		return fmt.Errorf("registry: provisional entity requires a positive identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := e.EntityID()
	if r.entities[id] != nil || r.provisional[id] != nil {
		return fmt.Errorf("entity %d already registered or being prepared", id)
	}
	r.provisional[id] = e
	if len(owner) == 1 {
		r.owners[id] = owner[0]
	}
	return nil
}

// CommitProvisional publishes the specified prepared entities atomically and
// queues their full spawn states. It does not call OnSpawn. Invalid or changed
// identities leave every entry provisional and all lifecycle queues unchanged.
func (r *Registry) CommitProvisional(ids ...int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		e := r.provisional[id]
		if seen[id] || e == nil || e.EntityID() != id || r.entities[id] != nil {
			return fmt.Errorf("registry: invalid provisional entity %d", id)
		}
		seen[id] = true
	}
	for _, id := range ids {
		e := r.provisional[id]
		delete(r.provisional, id)
		r.entities[id] = e
		r.spawned = append(r.spawned, e)
	}
	return nil
}

// DiscardProvisional removes a prepared entity and its owner without lifecycle
// hooks or replication output. Published entities are left unchanged.
func (r *Registry) DiscardProvisional(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.provisional[id]; ok {
		delete(r.provisional, id)
		delete(r.owners, id)
	}
}

func nilEntity(e Entity) bool {
	if e == nil {
		return true
	}
	v := reflect.ValueOf(e)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
