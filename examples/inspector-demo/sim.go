package main

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/demiurgos-hub/golem-engine/examples/inspector-demo/generated"
	"github.com/demiurgos-hub/golem-engine/golem"
)

// sim drives a small world that exercises every inspector field kind: moving
// entities, owner-only fields, lists, maps, custom types, spawns and removals.
type sim struct {
	rng       *rand.Rand
	wanderers []*generated.SyncedWanderer
	merchants []*generated.SyncedMerchant
	beacons   []*generated.SyncedBeacon
	elapsed   float64
	spawned   int
}

var moods = []string{"cheerful", "grumpy", "sleepy", "haggling"}

func newSim(seed uint64) *sim {
	return &sim{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// populate creates the initial world. Wanderers get a synthetic owner session
// so their gold is owner-only on the wire but still visible to the inspector.
func (s *sim) populate(srv *golem.Server, wanderers, merchants, beacons int) error {
	for i := 0; i < wanderers; i++ {
		if err := s.spawnWanderer(srv); err != nil {
			return err
		}
	}
	for i := 0; i < merchants; i++ {
		m := generated.NewSyncedMerchant(s.coord(), 0, s.coord(), fmt.Sprintf("Shop %d", i+1), moods[i%len(moods)], map[string]uint32{"apple": 10, "rope": 3})
		if err := srv.CreateEntity(m); err != nil {
			return err
		}
		s.merchants = append(s.merchants, m)
	}
	for i := 0; i < beacons; i++ {
		b := generated.NewSyncedBeacon(float32(i*40-40), 5, 0, 1)
		if err := srv.CreateEntity(b); err != nil {
			return err
		}
		s.beacons = append(s.beacons, b)
	}
	return nil
}

func (s *sim) coord() float32 { return float32(s.rng.Float64()*200 - 100) }

func (s *sim) spawnWanderer(srv *golem.Server) error {
	s.spawned++
	w := generated.NewSyncedWanderer(s.coord(), 0, s.coord(), fmt.Sprintf("Wanderer %d", s.spawned), 100, uint32(s.rng.IntN(50)),
		[]string{"new"}, map[string]int32{"str": int32(5 + s.rng.IntN(10)), "dex": int32(5 + s.rng.IntN(10))},
		[]generated.Item{{Name: "torch", Qty: 1}})
	if err := srv.CreateEntity(w, int64(100000+s.spawned)); err != nil {
		return err
	}
	s.wanderers = append(s.wanderers, w)
	return nil
}

// step advances the simulation; it runs on the tick goroutine.
func (s *sim) step(dt float64, srv *golem.Server) {
	s.elapsed += dt
	for i, w := range s.wanderers {
		x, y, z := w.Position3D()
		angle := s.elapsed*0.4 + float64(i)
		w.SetPosition3D(x+float32(math.Cos(angle)*dt*6), y, z+float32(math.Sin(angle)*dt*6))
		if s.rng.Float64() < 0.02 {
			w.SetHp(max(0, w.Hp()-int32(s.rng.IntN(15))))
		}
		if s.rng.Float64() < 0.01 {
			w.SetGold(w.Gold() + 1)
		}
		if s.rng.Float64() < 0.005 {
			if len(w.Tags()) > 2 {
				w.SetTags(nil) // exercises the cleared-collection path
			} else {
				w.SetTags(append(w.Tags(), []string{"tired", "lost", "brave"}[s.rng.IntN(3)]))
			}
		}
	}
	for i, m := range s.merchants {
		if s.rng.Float64() < 0.01 {
			m.SetMood(moods[s.rng.IntN(len(moods))])
		}
		if s.rng.Float64() < 0.01 {
			stock := map[string]uint32{}
			for k, v := range m.Stock() {
				stock[k] = v
			}
			stock["apple"] = uint32(s.rng.IntN(20))
			m.SetStock(stock)
		}
		_ = i
	}
	for i, b := range s.beacons {
		b.SetIntensity(float32(0.5 + 0.5*math.Sin(s.elapsed*2+float64(i))))
	}
	// Churn: occasionally retire a wounded wanderer and spawn a replacement.
	if len(s.wanderers) > 0 && s.rng.Float64() < 0.05 {
		i := s.rng.IntN(len(s.wanderers))
		if s.wanderers[i].Hp() < 60 {
			srv.DeleteEntity(s.wanderers[i].EntityID())
			s.wanderers = append(s.wanderers[:i], s.wanderers[i+1:]...)
			_ = s.spawnWanderer(srv)
		}
	}
}
