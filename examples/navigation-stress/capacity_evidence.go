package main

import (
	"context"
	"fmt"
	"math"

	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

const agentSpeed = 4.0

func capacityQueryLimits() navigation.Limits {
	return navigation.Limits{Nodes: 4096, PathPolygons: 2048, Corners: 512, Candidates: 4096}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type capacityRouteEvidence struct {
	Profile                      string
	Start, End                   navigation.Vec3
	LengthMetres, RequiredMetres float64
	Code, Reason                 navigation.Code
	Corners                      int
	PhysicalClearance            capacityClearanceEvidence
}

func capacityRequiredDistance(o options) float64 {
	return agentSpeed*(o.Warmup+o.Duration).Seconds()*1.1 + 100
}

func capacityPreflight(o options, f worldgen.Fixture, worlds []*dynamic.World) ([]capacityRouteEvidence, error) {
	if f.Config.Family != "capacity" {
		return nil, nil
	}
	if o.Churn != 0 || o.Edits != 0 || o.Burst != 0 {
		return nil, fmt.Errorf("capacity workload requires -churn=0 -edits-per-sec=0 -burst=0")
	}
	if len(f.Anchors) != 2 || len(f.Scene.Links) != 0 {
		return nil, fmt.Errorf("capacity workload requires exactly two endpoints and no traversal links")
	}
	var evidence []capacityRouteEvidence
	bodyProfiles, err := fixtureBodyProfiles(f)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]geom.Profile, len(bodyProfiles))
	for _, profile := range bodyProfiles {
		byID[profile.ID] = profile
	}
	var physicalFailure error
	for _, world := range worlds {
		q, err := world.Snapshot().NewQuery(capacityQueryLimits())
		if err != nil {
			return evidence, err
		}
		endpoints := [2]navigation.Surface{}
		for i, point := range f.Anchors {
			hit := q.Project(context.Background(), point, navigation.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}, navigation.Filter{})
			if hit.Code != navigation.Complete {
				return evidence, fmt.Errorf("capacity profile %s endpoint%d: %+v", world.Snapshot().Profile().ID, i, hit.Status)
			}
			endpoints[i] = hit.Surface
		}
		for direction := 0; direction < 2; direction++ {
			start, end := endpoints[direction], endpoints[1-direction]
			result := q.FindPath(context.Background(), start, end, navigation.Filter{})
			record := capacityRouteEvidence{Profile: world.Snapshot().Profile().ID, Start: start.Position(), End: end.Position(), RequiredMetres: capacityRequiredDistance(o), Code: result.Code, Reason: result.Reason}
			if result.Path != nil {
				last := start.Position()
				for _, corner := range result.Path.Corners() {
					dx, dy, dz := corner.Position.X-last.X, corner.Position.Y-last.Y, corner.Position.Z-last.Z
					record.LengthMetres += math.Sqrt(dx*dx + dy*dy + dz*dz)
					last = corner.Position
				}
				record.Corners = result.Path.CornerCount()
				body, exists := byID[record.Profile]
				if !exists {
					return append(evidence, record), fmt.Errorf("missing physical body for capacity profile %q", record.Profile)
				}
				record.PhysicalClearance, err = capacityPhysicalClearance(start.Position(), result.Path.Corners(), body, f.Scene.Volumes)
				if err != nil {
					return append(evidence, record), err
				}
				if contact := record.PhysicalClearance.FirstViolation; contact != nil && physicalFailure == nil {
					physicalFailure = fmt.Errorf("capacity physical clearance: profile %s direction %d segment %d obstacle %q separation %.12g metres (radius %.12g): %+v -> %+v", record.Profile, direction, contact.Segment, contact.Obstacle, contact.MinimumSeparationMetres, contact.Radius, contact.From, contact.To)
				}
			}
			evidence = append(evidence, record)
			if result.Code != navigation.Complete || result.Path == nil || result.Path.End().Position() != end.Position() || record.LengthMetres < record.RequiredMetres {
				return evidence, fmt.Errorf("capacity route insufficient: %+v", record)
			}
		}
	}
	return evidence, physicalFailure
}
