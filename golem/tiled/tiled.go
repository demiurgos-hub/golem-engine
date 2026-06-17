// Package tiled provides types and a loader for Tiled map files (.tmj JSON format).
// It is a server-side utility: use the parsed Map for collision grids, spawn
// detection, pathfinding, and similar game-logic needs. Client delivery of map
// data requires the schema/codegen integration (Phase 2+).
package tiled

import (
	"encoding/json"
	"fmt"
	"os"
)

// Map is the top-level Tiled map document parsed from a .tmj file.
type Map struct {
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	TileWidth    int        `json:"tilewidth"`
	TileHeight   int        `json:"tileheight"`
	Infinite     bool       `json:"infinite"`
	Orientation  string     `json:"orientation"`
	RenderOrder  string     `json:"renderorder"`
	Layers       []Layer    `json:"layers"`
	Tilesets     []Tileset  `json:"tilesets"`
	Properties   []Property `json:"properties"`
	Version      string     `json:"version"`
	TiledVersion string     `json:"tiledversion"`
}

// Layer represents a single map layer. The Type field discriminates between
// layer kinds: "tilelayer", "objectgroup", "imagelayer", and "group".
type Layer struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Visible    bool       `json:"visible"`
	Opacity    float64    `json:"opacity"`
	OffsetX    float64    `json:"offsetx"`
	OffsetY    float64    `json:"offsety"`
	Width      int        `json:"width"`   // tile columns (tilelayer only)
	Height     int        `json:"height"`  // tile rows (tilelayer only)
	Data       []int      `json:"data"`    // tile GIDs (tilelayer only)
	Objects    []Object   `json:"objects"` // (objectgroup only)
	Layers     []Layer    `json:"layers"`  // child layers (group only)
	Image      string     `json:"image"`   // image path (imagelayer only)
	Properties []Property `json:"properties"`
}

// Object is a Tiled map object inside an objectgroup layer.
type Object struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	X          float64    `json:"x"`
	Y          float64    `json:"y"`
	Width      float64    `json:"width"`
	Height     float64    `json:"height"`
	Rotation   float64    `json:"rotation"`
	Visible    bool       `json:"visible"`
	GID        int        `json:"gid"` // tile GID for tile objects; 0 if not a tile object
	Ellipse    bool       `json:"ellipse"`
	Point      bool       `json:"point"`
	Properties []Property `json:"properties"`
}

// Tileset describes a tileset referenced by the map.
// When Source is non-empty the tileset is external (.tsx) and the remaining
// fields may be zero — the consumer is responsible for loading the source file.
type Tileset struct {
	FirstGID    int        `json:"firstgid"`
	Source      string     `json:"source"`
	Name        string     `json:"name"`
	TileWidth   int        `json:"tilewidth"`
	TileHeight  int        `json:"tileheight"`
	Spacing     int        `json:"spacing"`
	Margin      int        `json:"margin"`
	Columns     int        `json:"columns"`
	TileCount   int        `json:"tilecount"`
	Image       string     `json:"image"`
	ImageWidth  int        `json:"imagewidth"`
	ImageHeight int        `json:"imageheight"`
	Properties  []Property `json:"properties"`
}

// Property is a custom property attached to a map, layer, object, or tileset.
// The Type field is one of: "string", "int", "float", "bool", "color", "file",
// "object". Value is the raw JSON value; use the typed accessors to convert it.
type Property struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// StringValue returns the property value as a string, or "" if not a string.
func (p Property) StringValue() string {
	s, _ := p.Value.(string)
	return s
}

// BoolValue returns the property value as a bool, or false if not a bool.
func (p Property) BoolValue() bool {
	b, _ := p.Value.(bool)
	return b
}

// FloatValue returns the property value as float64. JSON numbers unmarshal as
// float64, so this works for both "int" and "float" typed properties.
func (p Property) FloatValue() float64 {
	f, _ := p.Value.(float64)
	return f
}

// IntValue returns the property value as int. JSON numbers unmarshal as float64
// so the conversion is lossy for values beyond float64 precision — use
// FloatValue for very large integers.
func (p Property) IntValue() int {
	return int(p.FloatValue())
}

// Load reads a Tiled map from a .tmj file and returns the parsed Map.
func Load(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tiled: reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes a Tiled map from raw .tmj JSON bytes.
func Parse(data []byte) (*Map, error) {
	var m Map
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("tiled: parsing map: %w", err)
	}
	return &m, nil
}

// LayerByName returns the first top-level layer with the given name, or nil.
func (m *Map) LayerByName(name string) *Layer {
	for i := range m.Layers {
		if m.Layers[i].Name == name {
			return &m.Layers[i]
		}
	}
	return nil
}

// PropertyByName returns the named property from the map's custom properties,
// or the zero Property if not found.
func (m *Map) PropertyByName(name string) (Property, bool) {
	return propertyByName(m.Properties, name)
}

// PropertyByName returns the named property from the layer's custom properties,
// or the zero Property if not found.
func (l *Layer) PropertyByName(name string) (Property, bool) {
	return propertyByName(l.Properties, name)
}

// PropertyByName returns the named property from the object's custom properties.
func (o *Object) PropertyByName(name string) (Property, bool) {
	return propertyByName(o.Properties, name)
}

func propertyByName(props []Property, name string) (Property, bool) {
	for _, p := range props {
		if p.Name == name {
			return p, true
		}
	}
	return Property{}, false
}
