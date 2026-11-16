package objecttemplate

import (
	"errors"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestDecodeRejectsInvalidCatalogs(t *testing.T) {
	cases := map[string]func(*Catalog){
		"generator":          func(c *Catalog) { c.Generator = "other" },
		"dimensions":         func(c *Catalog) { c.Dimensions = 4 },
		"duplicate template": func(c *Catalog) { c.Templates[1].ID = "rock" },
		"empty template":     func(c *Catalog) { c.Templates[1].Colliders = nil },
		"dynamic body":       func(c *Catalog) { c.Templates[1].Bodies[0].Kind = "dynamic" },
		"missing asset":      func(c *Catalog) { c.Templates[1].Colliders[0].AssetID = "absent" },
		"navigation asset":   func(c *Catalog) { c.Templates[0].Navigation.Instances[0].AssetID = "absent" },
		"physics on float64": func(c *Catalog) { c.Templates[1].Colliders[0].AssetID = "nav-plane" },
		"2D shapes in 3D": func(c *Catalog) {
			c.Templates[1].Shapes = []Shape2D{{ID: "s", Kind: ShapeCircle, Radius: 1, Layer: "x"}}
		},
		"layer matrix": func(c *Catalog) { c.LayerCollisionMasks = c.LayerCollisionMasks[:4] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := testCatalogDocument()
			mutate(&c)
			if _, err := decodeCatalog(t, c, testAssets()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestDecodeRejectsTrailingDataAndUnknownFields(t *testing.T) {
	for _, document := range []string{
		`{"Generator":"` + Generator + `","Version":1,"CatalogID":"x","Revision":1,"Dimensions":2,"Templates":[]} {}`,
		`{"Generator":"` + Generator + `","Version":1,"CatalogID":"x","Revision":1,"Dimensions":2,"Templates":[],"Extra":1}`,
	} {
		if _, _, err := Decode(strings.NewReader(document), nil, DefaultLimits()); err == nil {
			t.Fatalf("accepted %s", document)
		}
	}
}

func TestNavigationAssetsExpandOnlyReferencedGeometry(t *testing.T) {
	catalog := testCatalog(t)
	assets := catalog.NavigationAssets()
	if len(assets) != 2 || assets[0].ID != "ground" || assets[1].ID != "nav-plane" {
		t.Fatalf("navigation assets %+v", assets)
	}
	if len(assets[0].Vertices) != 9 || len(assets[0].Triangles) != 8 {
		t.Fatal("heightfield not expanded for navigation")
	}
}

func TestPlaceNavigationTransformsElements(t *testing.T) {
	rock, _ := testCatalog(t).Template("rock")
	nav, err := rock.PlaceNavigation("r1", At(p.Vec3{X: 10, Y: 1}, yaw(90)), 4)
	if err != nil {
		t.Fatal(err)
	}
	instance := nav.Instances[0]
	if instance.ID != "r1/root/nav" || instance.Revision != 4 {
		t.Fatalf("instance identity %+v", instance)
	}
	if point := instance.Transform.Point(geom.Vec3{X: 1}); !near(float32(point.X), 10) || !near(float32(point.Y), 1) || !near(float32(point.Z), -1) {
		t.Fatalf("instance transform maps (1,0,0) to %+v", point)
	}
	box := nav.Volumes[0]
	if box.Shape != "box" || !near(float32(box.Bounds.Min.X), 8) || !near(float32(box.Bounds.Max.X), 12) || !near(float32(box.Bounds.Min.Z), -1) || !near(float32(box.Bounds.Max.Y), 3) {
		t.Fatalf("quarter-turn box %+v", box.Bounds)
	}
	if link := nav.Links[0]; !near(float32(link.End.Z), -2) || link.ID != "r1/root/link" {
		t.Fatalf("link %+v", link)
	}

	nav, err = rock.PlaceNavigation("r2", At(p.Vec3{}, yaw(45)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if v := nav.Volumes[0]; v.Shape != "convex" || len(v.Points) != 4 {
		t.Fatalf("diagonal box should become convex, got %+v", v)
	}
	if err = (geom.Scene{Assets: testCatalog(t).NavigationAssets(), Instances: nav.Instances, Volumes: nav.Volumes, Links: nav.Links}).Validate(geom.Vec3{}, true); err != nil {
		t.Fatal(err)
	}
	removal := nav.Removal()
	if len(removal.RemoveInstances) != 1 || removal.RemoveVolumes[0] != "r2/root/area" || removal.RemoveLinks[0] != "r2/root/link" {
		t.Fatalf("removal %+v", removal)
	}

	if _, err = rock.PlaceNavigation("r3", At(p.Vec3{}, p.Quat{X: .38268343, W: .9238795}), 1); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("pitched volume: %v", err)
	}
	copyOnly, _ := testCatalog(t).Template("rock-copy")
	if nav, err = copyOnly.PlaceNavigation("c", At(p.Vec3{}, p.Quat{W: 1}), 1); err != nil || len(nav.Instances) != 0 {
		t.Fatal("template without navigation", err)
	}
}

func TestDecodeKeepsAssetsOutsideTheSidecarUnreachable(t *testing.T) {
	c := testCatalogDocument()
	assets := testAssets()
	assets[0] = cubeAsset("other", geometry.MeshFloat32)
	if _, err := decodeCatalog(t, c, assets); err == nil {
		t.Fatal("decoded a template whose asset is missing from the sidecar")
	}
}
