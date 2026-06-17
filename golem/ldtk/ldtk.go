// Package ldtk provides types and a loader for LDtk project files (.ldtk JSON format).
// It is a server-side utility: use the parsed Project for level geometry, entity
// spawns, int-grid collision, and similar game-logic needs. Client delivery of map
// data requires the schema/codegen integration (Phase 2+).
package ldtk

import (
	"encoding/json"
	"fmt"
	"os"
)

// Project is the top-level LDtk document parsed from a .ldtk file.
// When multi-world mode is active, levels are nested inside Worlds; otherwise
// they appear directly in Levels.
type Project struct {
	JSONVersion string  `json:"jsonVersion"`
	BGColor     string  `json:"bgColor"`
	Worlds      []World `json:"worlds"`
	Levels      []Level `json:"levels"` // populated in single-world mode
	Defs        Defs    `json:"defs"`
}

// World groups levels in multi-world mode.
type World struct {
	Identifier  string  `json:"identifier"`
	WorldLayout string  `json:"worldLayout"`
	Levels      []Level `json:"levels"`
}

// Level is a single LDtk level (room / map area).
type Level struct {
	Identifier     string          `json:"identifier"`
	UID            int             `json:"uid"`
	WorldX         int             `json:"worldX"`
	WorldY         int             `json:"worldY"`
	PxWidth        int             `json:"pxWid"`
	PxHeight       int             `json:"pxHei"`
	BGColor        string          `json:"bgColor"`
	LayerInstances []LayerInstance `json:"layerInstances"`
	FieldInstances []FieldInstance `json:"fieldInstances"`
}

// LayerInstance is one layer as it exists in a specific level.
// Type is one of: "Tiles", "IntGrid", "Entities", "AutoLayer".
type LayerInstance struct {
	Identifier      string           `json:"__identifier"`
	Type            string           `json:"__type"`
	CellsWide       int              `json:"__cWid"`
	CellsHigh       int              `json:"__cHei"`
	GridSize        int              `json:"__gridSize"`
	GridTiles       []Tile           `json:"gridTiles"`
	AutoLayerTiles  []Tile           `json:"autoLayerTiles"`
	EntityInstances []EntityInstance `json:"entityInstances"`
	IntGridCSV      []int            `json:"intGridCsv"`
}

// Tile is one rendered tile inside a Tiles or AutoLayer layer instance.
type Tile struct {
	Px  [2]int  `json:"px"`  // pixel position [x, y] in the level
	Src [2]int  `json:"src"` // top-left pixel in the source tileset [x, y]
	F   int     `json:"f"`   // flip flags (0=none, 1=X, 2=Y, 3=XY)
	T   int     `json:"t"`   // tile ID in the tileset
	A   float64 `json:"a"`   // opacity (0–1)
}

// EntityInstance is a placed entity inside an Entities layer.
type EntityInstance struct {
	Identifier     string          `json:"__identifier"`
	Grid           [2]int          `json:"__grid"`   // grid-cell position [col, row]
	WorldX         int             `json:"__worldX"` // pixel position in the world
	WorldY         int             `json:"__worldY"`
	Width          int             `json:"width"`
	Height         int             `json:"height"`
	Px             [2]int          `json:"px"` // pixel position within the level
	FieldInstances []FieldInstance `json:"fieldInstances"`
}

// FieldInstance is a custom field value on a Level or EntityInstance.
// Value is the raw JSON value; use the typed accessors to convert it.
type FieldInstance struct {
	Identifier string `json:"__identifier"`
	Type       string `json:"__type"`
	Value      any    `json:"__value"`
}

// StringValue returns the field value as a string, or "" if not a string.
func (f FieldInstance) StringValue() string {
	s, _ := f.Value.(string)
	return s
}

// BoolValue returns the field value as a bool, or false if not a bool.
func (f FieldInstance) BoolValue() bool {
	b, _ := f.Value.(bool)
	return b
}

// FloatValue returns the field value as float64. JSON numbers unmarshal as
// float64, so this works for both integer and float fields.
func (f FieldInstance) FloatValue() float64 {
	v, _ := f.Value.(float64)
	return v
}

// IntValue returns the field value as int.
func (f FieldInstance) IntValue() int {
	return int(f.FloatValue())
}

// Defs holds the project-level definitions for layers, entities, tilesets, and enums.
type Defs struct {
	Layers   []LayerDef   `json:"layers"`
	Entities []EntityDef  `json:"entities"`
	Tilesets []TilesetDef `json:"tilesets"`
	Enums    []EnumDef    `json:"enums"`
}

// LayerDef is the definition of a layer type used across levels.
type LayerDef struct {
	Identifier string `json:"identifier"`
	Type       string `json:"type"`
	GridSize   int    `json:"gridSize"`
	UID        int    `json:"uid"`
}

// EntityDef is the definition of an entity type.
type EntityDef struct {
	Identifier string `json:"identifier"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	UID        int    `json:"uid"`
}

// TilesetDef describes a tileset used by the project.
type TilesetDef struct {
	Identifier   string `json:"identifier"`
	RelPath      string `json:"relPath"`
	TileGridSize int    `json:"tileGridSize"`
	CellsWide    int    `json:"__cWid"`
	CellsHigh    int    `json:"__cHei"`
	UID          int    `json:"uid"`
}

// EnumDef describes an enum type defined in the project.
type EnumDef struct {
	Identifier string      `json:"identifier"`
	Values     []EnumValue `json:"values"`
	UID        int         `json:"uid"`
}

// EnumValue is one value inside an EnumDef.
type EnumValue struct {
	ID     string `json:"id"`
	TileID int    `json:"tileId"`
}

// Load reads an LDtk project from a .ldtk file and returns the parsed Project.
func Load(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ldtk: reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes an LDtk project from raw .ldtk JSON bytes.
func Parse(data []byte) (*Project, error) {
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("ldtk: parsing project: %w", err)
	}
	return &p, nil
}

// AllLevels returns every level in the project regardless of whether multi-world
// mode is active. Single-world projects put levels in Project.Levels; multi-world
// projects nest them inside Project.Worlds.
func (p *Project) AllLevels() []Level {
	if len(p.Worlds) > 0 {
		var out []Level
		for _, w := range p.Worlds {
			out = append(out, w.Levels...)
		}
		return out
	}
	return p.Levels
}

// LevelByIdentifier returns the first level with the given identifier across all
// worlds, or nil if not found.
func (p *Project) LevelByIdentifier(id string) *Level {
	for i := range p.Levels {
		if p.Levels[i].Identifier == id {
			return &p.Levels[i]
		}
	}
	for i := range p.Worlds {
		for j := range p.Worlds[i].Levels {
			if p.Worlds[i].Levels[j].Identifier == id {
				return &p.Worlds[i].Levels[j]
			}
		}
	}
	return nil
}

// LayerByIdentifier returns the first layer instance with the given identifier,
// or nil if not found.
func (l *Level) LayerByIdentifier(id string) *LayerInstance {
	for i := range l.LayerInstances {
		if l.LayerInstances[i].Identifier == id {
			return &l.LayerInstances[i]
		}
	}
	return nil
}

// FieldByIdentifier returns the named field instance on the level, or nil.
func (l *Level) FieldByIdentifier(id string) *FieldInstance {
	return fieldByIdentifier(l.FieldInstances, id)
}

// FieldByIdentifier returns the named field instance on the entity, or nil.
func (e *EntityInstance) FieldByIdentifier(id string) *FieldInstance {
	return fieldByIdentifier(e.FieldInstances, id)
}

func fieldByIdentifier(fields []FieldInstance, id string) *FieldInstance {
	for i := range fields {
		if fields[i].Identifier == id {
			return &fields[i]
		}
	}
	return nil
}
