package interest

import (
	"fmt"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

func BenchmarkManagerComputeDiffs(b *testing.B) {
	b.Run("stable_200sessions_1000nearby", func(b *testing.B) {
		reg := registry.NewRegistry()
		mgr := NewManager(4)

		const sessionCount = 200
		const nearbyCount = 1000

		for i := 0; i < sessionCount; i++ {
			id := int64(i + 1)
			e := &testEntity{
				id:   id,
				x:    float64(i % 10),
				y:    float64(i / 10),
				full: []byte(fmt.Sprintf("player-%d", id)),
			}
			if err := reg.Add(e); err != nil {
				b.Fatalf("Add anchor %d: %v", id, err)
			}
			mgr.AssignFOI(id, id, 12, 2)
		}

		for i := 0; i < nearbyCount; i++ {
			id := int64(sessionCount + i + 1)
			e := &testEntity{
				id:   id,
				x:    float64(i % 20),
				y:    float64(i / 20),
				full: []byte(fmt.Sprintf("mob-%d", id)),
			}
			if err := reg.Add(e); err != nil {
				b.Fatalf("Add mob %d: %v", id, err)
			}
		}

		mgr.UpdateGrid(reg)
		mgr.ComputeDiffs()

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mgr.UpdateGrid(reg)
			mgr.ComputeDiffs()
		}
	})

	b.Run("stable_50sessions_200nearby", func(b *testing.B) {
		reg := registry.NewRegistry()
		mgr := NewManager(4)

		const sessionCount = 50
		const nearbyCount = 200

		for i := 0; i < sessionCount; i++ {
			id := int64(i + 1)
			e := &testEntity{
				id:   id,
				x:    float64(i % 10),
				y:    float64(i / 10),
				full: []byte(fmt.Sprintf("player-%d", id)),
			}
			if err := reg.Add(e); err != nil {
				b.Fatalf("Add anchor %d: %v", id, err)
			}
			mgr.AssignFOI(id, id, 10, 2)
		}

		for i := 0; i < nearbyCount; i++ {
			id := int64(sessionCount + i + 1)
			e := &testEntity{
				id:   id,
				x:    float64(i % 20),
				y:    float64(i / 20),
				full: []byte(fmt.Sprintf("mob-%d", id)),
			}
			if err := reg.Add(e); err != nil {
				b.Fatalf("Add mob %d: %v", id, err)
			}
		}

		mgr.UpdateGrid(reg)
		mgr.ComputeDiffs()

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mgr.UpdateGrid(reg)
			mgr.ComputeDiffs()
		}
	})

	b.Run("churn_50sessions_unique_candidates", func(b *testing.B) {
		reg := registry.NewRegistry()
		mgr := NewManager(4)

		const sessionCount = 50

		for i := 0; i < sessionCount; i++ {
			id := int64(i + 1)
			e := &testEntity{
				id:   id,
				x:    float64(i % 10),
				y:    float64(i / 10),
				full: []byte(fmt.Sprintf("player-%d", id)),
			}
			if err := reg.Add(e); err != nil {
				b.Fatalf("Add anchor %d: %v", id, err)
			}
			mgr.AssignFOI(id, id, 10, 2)
		}

		nextID := int64(10_000)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			movers := make([]int64, 0, sessionCount)
			for j := 0; j < sessionCount; j++ {
				id := nextID
				nextID++
				movers = append(movers, id)
				e := &testEntity{
					id:   id,
					x:    float64(j % 10),
					y:    float64(j / 10),
					full: []byte(fmt.Sprintf("spawn-%d", id)),
				}
				if err := reg.Add(e); err != nil {
					b.Fatalf("Add churn entity %d: %v", id, err)
				}
			}

			mgr.UpdateGrid(reg)
			mgr.ComputeDiffs()

			for _, id := range movers {
				reg.DeleteEntity(id)
			}
			mgr.UpdateGrid(reg)
			mgr.ComputeDiffs()
		}
	})
}
