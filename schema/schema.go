package schema

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaFile is the YAML model for one entity schema (e.g. schemas/entities/player.yaml).
type SchemaFile struct {
	Entity     string                  `yaml:"entity"`
	Global     bool                    `yaml:"global"`               // true = always replicated to every client (bypasses FOI)
	Persistent *bool                   `yaml:"persistent,omitempty"` // nil = default true; false = omit from snapshots
	Vars       map[string]SchemaVarDef `yaml:"vars"`
}

// SchemaVarDef defines a single synced variable in a schema.
type SchemaVarDef struct {
	Type string `yaml:"type"`
	Sync string `yaml:"sync"` // "tick" (default) or "once"
	Tag  int    `yaml:"tag"`  // required user-slot index (1-based); proto field = Tag + 3 for entity vars
}

// VarInfo holds all computed metadata for a single synced variable across languages.
type VarInfo struct {
	SnakeName     string
	GoName        string
	FieldName     string
	GoType        string
	ProtoType     string
	ProtoHelper   string
	TSType        string
	TSDefault     string
	TSPublicName  string
	TSPrivateName string
	TSProtoField  string
	CSType        string
	CSDefault     string
	CSPublicName  string
	CSPrivateName string
	CSProtoField  string
	UserTag       int // user-facing tag from YAML (1-based slot index)
	ProtoTag      int // wire-format field number (UserTag + per-type offset)
	BitIndex      int
	Sync          string
	// Collection fields (non-zero when IsRepeated or IsMap is true)
	IsRepeated      bool
	IsMap           bool
	MapKeyProtoType string // e.g. "string" — key type for dict
	ElemProtoType   string // element proto type name (scalar or custom type name)
	ElemIsCustom    bool   // true when ElemProtoType refers to a CustomTypeData
	ElemGoType      string // Go type of collection element (e.g. "Item" or "int32")
	MapKeyGoType    string // Go type of map key (e.g. "string")
	ElemTSType      string // TS type of collection element (e.g. "Item" or "number")
	MapKeyTSType    string // TS type of map key (e.g. "string")
	ElemCSType      string // C# type of collection element (e.g. "Item" or "int")
	MapKeyCSType    string // C# type of map key (e.g. "string")
}

// TSConvert returns the expression to read this field from a proto message variable.
func (v VarInfo) TSConvert(src string) string {
	if v.ProtoType == "int64" || v.ProtoType == "sint64" || v.ProtoType == "uint64" {
		return fmt.Sprintf("Number(%s.%s)", src, v.TSProtoField)
	}
	return fmt.Sprintf("%s.%s", src, v.TSProtoField)
}

// CSConvert returns the expression to read this field from a generated C# proto model.
func (v VarInfo) CSConvert(src string) string {
	return fmt.Sprintf("%s.%s", src, v.CSProtoField)
}

// EntityData is the fully-resolved template data for one entity type.
type EntityData struct {
	Name            string
	LowerName       string
	Dimensions      int
	Is3D            bool
	Global          bool // always replicated to every client (bypasses FOI)
	Persistent      bool // false = omit this entity type from world snapshots
	AllVars         []VarInfo
	TickVars        []VarInfo
	OnceVars        []VarInfo
	UsedCustomTypes []CustomTypeData // deduplicated custom types referenced by this entity's vars
	CustomTypeNames []string         // deduplicated custom type names referenced by this entity's vars
	Events          []EventData      // server events targeting this entity type
	ProtocolImport  string
	GolemImport     string
	GoPackage       string // Go package name for generated *_synced.go (e.g. "generated")
}

// EntityUpdateField is one oneof entry in the generated EntityUpdate message.
type EntityUpdateField struct {
	MessageType string
	SnakeName   string
	Tag         int
}

// WorldSchemaFile is the YAML model for one world data type (e.g. schemas/world/zone.yaml).
type WorldSchemaFile struct {
	World  string                   `yaml:"world"`
	Fields map[string]WorldFieldDef `yaml:"fields"`
	Source *WorldSourceDef          `yaml:"source"` // optional; links to a Tiled or LDtk file
}

// WorldFieldDef defines a single field in a world data schema.
type WorldFieldDef struct {
	Type string `yaml:"type"`
}

// WorldSourceDef links a world schema to a Tiled (.tmj), LDtk (.ldtk), or catalog YAML file.
// When present, bake reads the file at code-generation time to validate it exists,
// and generates a LoadXxxData() helper that populates the struct at server startup.
type WorldSourceDef struct {
	Format    string            `yaml:"format"`     // "tiled", "ldtk", or "catalog"
	File      string            `yaml:"file"`       // path to the source file, relative to project root
	Extract   []WorldExtractDef `yaml:"extract"`    // tiled/ldtk only: custom map properties to expose as typed proto fields
	URLPrefix string            `yaml:"url_prefix"` // tiled/ldtk only: emit map_url string instead of bytes tile_data
	Type      string            `yaml:"type"`       // catalog only: custom type name (from schemas/types/)
	Key       string            `yaml:"key"`        // catalog only: field on Type to use as map key; absent = list
}

// WorldExtractDef declares one custom map property to extract as a typed proto field.
type WorldExtractDef struct {
	Name string `yaml:"name"` // property name (snake_case)
	Type string `yaml:"type"` // proto scalar type (e.g. "int32", "float", "string")
}

// WorldFieldInfo holds computed metadata for a single world data field across languages.
type WorldFieldInfo struct {
	SnakeName    string
	GoName       string
	FieldName    string // camelCase
	GoType       string
	ProtoType    string
	TSType       string
	TSDefault    string
	TSProtoField string
	CSType       string
	CSDefault    string
	CSProtoField string
	ProtoTag     int
	// Collection fields (non-zero when IsRepeated or IsMap is true)
	IsRepeated      bool
	IsMap           bool
	MapKeyProtoType string
	MapKeyGoType    string
	MapKeyTSType    string
	MapKeyCSType    string
	ElemProtoType   string
	ElemIsCustom    bool
	ElemGoType      string
	ElemTSType      string
	ElemCSType      string
}

// WorldTypeData is the fully-resolved template data for one world data type.
type WorldTypeData struct {
	Name           string // PascalCase, e.g. "Zone"
	DataName       string // Name + "Data", e.g. "ZoneData"
	SnakeName      string // snake_case of DataName, e.g. "zone_data"
	LowerName      string // lcFirst of Name, e.g. "zone"
	Fields         []WorldFieldInfo
	Source         *WorldSourceDef // non-nil when the schema has a source: block
	ProtocolImport string
	GolemImport    string
	// Catalog-specific fields (non-zero when IsCatalog is true)
	IsCatalog        bool
	CatalogKeyField  string // snake_case field name used as map key; empty = list
	CatalogKeyTSType string // TS type of the key field (e.g. "number"), for generated helpers
	CatalogKeyCSType string // C# type of the key field (e.g. "int"), for generated helpers
	CatalogItemType  string // custom type name (e.g. "Item"), for generated helpers
}

// WorldUpdateField is one oneof entry in the generated WorldUpdate message.
type WorldUpdateField struct {
	MessageType string // e.g. "ZoneData"
	SnakeName   string // e.g. "zone_data"
	Tag         int
}

// ProtoTemplateData is passed to the entities.proto template.
type ProtoTemplateData struct {
	Package             string
	GoPackage           string
	Dimensions          int
	Is3D                bool
	Entities            []EntityData
	EntityUpdateFields  []EntityUpdateField
	EntityRemovedTag    int
	Commands            []CommandData
	ClientMessageFields []ClientMessageField
	WorldTypes          []WorldTypeData
	WorldUpdateFields   []WorldUpdateField
	Events              []EventData
	ServerEventFields   []ServerEventField
	CustomTypes         []CustomTypeData // all project-wide custom types, for proto message generation
}

// SharedData is passed to once-per-bake shared templates (e.g. MarshalEntityRemoved,
// EntityManager, CommandRouter). Includes all entities, commands, world types, and events.
type SharedData struct {
	ProtocolImport          string
	GolemImport             string
	GoPackage               string
	Dimensions              int
	Is3D                    bool
	Entities                []EntityData
	Commands                []CommandData
	WorldTypes              []WorldTypeData
	Events                  []EventData
	CustomTypes             []CustomTypeData
	WorldCatalogCustomTypes []string // deduplicated custom type names used by catalog world fields
	Fingerprint             string   // SHA-256 of all entity schemas at bake time
}

// WorldCatalogCustomTypeNames returns the deduplicated, sorted list of custom
// type names (e.g. "ItemType") referenced as catalog item types across all
// world types. Used by templates that need to import those names from entities_pb.
func WorldCatalogCustomTypeNames(worldTypes []WorldTypeData) []string {
	seen := map[string]bool{}
	var names []string
	for _, wt := range worldTypes {
		if wt.IsCatalog && wt.CatalogItemType != "" && !seen[wt.CatalogItemType] {
			seen[wt.CatalogItemType] = true
			names = append(names, wt.CatalogItemType)
		}
	}
	slices.Sort(names)
	return names
}

// TypeSchemaFile is the YAML model for one custom type definition (e.g. schemas/types/item.yaml).
type TypeSchemaFile struct {
	Type   string                  `yaml:"type"`
	Fields map[string]TypeFieldDef `yaml:"fields"`
}

// TypeFieldDef defines a single field in a custom type.
// Fields must use the explicit object form `{ tag: N, type: T }` — a tag is required.
type TypeFieldDef struct {
	Type string `yaml:"type"`
	Tag  int    `yaml:"tag"` // required user-slot index (1-based); proto field = Tag (no offset for custom types)
}

// CustomFieldInfo holds computed metadata for a single field in a custom type.
type CustomFieldInfo struct {
	SnakeName string
	GoName    string
	FieldName string
	GoType    string
	ProtoType string
	TSType    string
	TSDefault string
	CSType    string
	CSDefault string
	ProtoTag  int
}

// CustomTypeData is the fully-resolved template data for one custom type.
type CustomTypeData struct {
	Name   string
	Fields []CustomFieldInfo
}

// LoadCustomTypes reads all .yaml files from the types directory and returns
// parsed custom types. Returns an empty slice (not an error) if the directory
// does not exist, so custom types are optional.
func LoadCustomTypes(typesDir string) ([]CustomTypeData, error) {
	entries, err := os.ReadDir(typesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading types dir %s: %w", typesDir, err)
	}

	seen := make(map[string]string)
	var types []CustomTypeData
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(typesDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var tf TypeSchemaFile
		if err := yaml.Unmarshal(data, &tf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		name := strings.TrimSpace(tf.Type)
		if name == "" {
			return nil, fmt.Errorf("%s: type schema must set a non-empty type name", e.Name())
		}
		if prev, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate custom type %q in %s and %s", name, prev, e.Name())
		}
		seen[name] = e.Name()
		types = append(types, BuildCustomTypeData(tf))
	}
	slices.SortFunc(types, func(a, b CustomTypeData) int {
		return strings.Compare(a.Name, b.Name)
	})
	return types, nil
}

// BuildCustomTypeData converts a parsed type schema file into template-ready data.
// Each field must have an explicit tag (≥ 1); proto field number equals the tag directly (no offset).
func BuildCustomTypeData(tf TypeSchemaFile) CustomTypeData {
	ct := CustomTypeData{Name: tf.Type}
	keys := SortedKeys(tf.Fields)

	// Validate: tags required and unique.
	seenTags := make(map[int]string, len(keys))
	for _, k := range keys {
		def := tf.Fields[k]
		if def.Tag == 0 {
			log.Fatalf("custom type %q: field %q: tag is required", tf.Type, k)
		}
		if prev, ok := seenTags[def.Tag]; ok {
			log.Fatalf("custom type %q: fields %q and %q share tag %d", tf.Type, prev, k, def.Tag)
		}
		seenTags[def.Tag] = k
	}

	// Process fields in tag order for stable output.
	slices.SortFunc(keys, func(a, b string) int { return tf.Fields[a].Tag - tf.Fields[b].Tag })

	for _, k := range keys {
		def := tf.Fields[k]
		goType, ok := protoToGo[def.Type]
		if !ok {
			log.Fatalf("unknown proto type %q for field %q in custom type %q", def.Type, k, tf.Type)
		}
		tsType := protoToTS[def.Type]
		csType := protoToCS[def.Type]
		ct.Fields = append(ct.Fields, CustomFieldInfo{
			SnakeName: k,
			GoName:    SnakeToPascal(k),
			FieldName: SnakeToCamel(k),
			GoType:    goType,
			ProtoType: def.Type,
			TSType:    tsType,
			TSDefault: tsDefaults[tsType],
			CSType:    csType,
			CSDefault: csDefaults[csType],
			ProtoTag:  def.Tag,
		})
	}
	return ct
}

// CommandSchemaFile is the YAML model for one command (e.g. schemas/commands/move.yaml).
type CommandSchemaFile struct {
	Command    string                     `yaml:"command"`
	Target     string                     `yaml:"target"`      // "entity" or "session"
	EntityType string                     `yaml:"entity_type"` // required when target is "entity"
	Fields     map[string]CommandFieldDef `yaml:"fields"`
}

// CommandFieldDef defines a single field in a command schema.
// Supports scalar shorthand (`dx: float`) and explicit (`dx: { type: float }`) YAML syntax.
type CommandFieldDef struct {
	Type string `yaml:"type"`
}

// UnmarshalYAML allows scalar shorthand (`dx: float`) as well as the explicit
// object form (`dx: { type: float }`).
func (c *CommandFieldDef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		c.Type = value.Value
		return nil
	}
	type plain CommandFieldDef
	return value.Decode((*plain)(c))
}

// CommandFieldInfo holds computed metadata for a single command field.
type CommandFieldInfo struct {
	SnakeName string
	GoName    string
	FieldName string // camelCase
	GoType    string
	ProtoType string
	TSType    string
	TSDefault string // TS default value expression; for custom types this is an object literal
	CSType    string
	CSDefault string // C# default value expression; for custom types this is a constructor expression
	IsCustom  bool   // true when the field type is a custom composite type, not a proto scalar
	ProtoTag  int
}

// CommandData is the fully-resolved template data for one command type.
type CommandData struct {
	Name       string
	LowerName  string
	Target     string // "entity" or "session"
	EntityType string // PascalCase entity name; set when Target is "entity"
	Fields     []CommandFieldInfo
}

// ClientMessageField is one oneof entry in the generated ClientMessage message.
type ClientMessageField struct {
	MessageType string
	SnakeName   string
	Tag         int
}

// LoadSchemas reads all .yaml files from the entity schemas directory and returns parsed entities.
// customTypes maps type name to resolved CustomTypeData for collection field validation.
func LoadSchemas(schemasDir string, dimensions int, customTypes map[string]CustomTypeData) ([]EntityData, error) {
	entries, err := os.ReadDir(schemasDir)
	if err != nil {
		return nil, fmt.Errorf("reading schemas dir %s: %w", schemasDir, err)
	}

	var entities []EntityData
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(schemasDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var sf SchemaFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		entities = append(entities, BuildEntityData(sf, dimensions, customTypes))
	}

	if len(entities) == 0 {
		return nil, fmt.Errorf("no entity schemas found in %s", schemasDir)
	}
	return entities, nil
}

// BuildEntityData converts a parsed schema file into template-ready entity data.
// Each var must carry an explicit tag (≥ 1). Proto field numbers are offset by
// the reserved entity metadata fields: entity_id, position components, and revision.
// Dirty-bit indices below dimensions are reserved for position components.
// customTypes maps type name to resolved CustomTypeData for collection field validation.
func BuildEntityData(sf SchemaFile, dimensions int, customTypes map[string]CustomTypeData) EntityData {
	if dimensions == 0 {
		dimensions = 2
	}
	if dimensions != 2 && dimensions != 3 {
		log.Fatalf("entity %q: dimensions must be 2 or 3, got %d", sf.Entity, dimensions)
	}

	reservedNames := []string{"pos_x", "pos_y", "revision"}
	if dimensions == 3 {
		reservedNames = append(reservedNames, "pos_z")
	}
	for _, reserved := range reservedNames {
		if _, ok := sf.Vars[reserved]; ok {
			log.Fatalf("entity %q: var name %q is reserved for implicit entity metadata", sf.Entity, reserved)
		}
	}

	persistent := true
	if sf.Persistent != nil {
		persistent = *sf.Persistent
	}

	ed := EntityData{
		Name:       sf.Entity,
		LowerName:  LcFirst(sf.Entity),
		Dimensions: dimensions,
		Is3D:       dimensions == 3,
		Global:     sf.Global,
		Persistent: persistent,
	}

	usedTypes := make(map[string]CustomTypeData)
	customTypeNames := make(map[string]bool)

	keys := SortedKeys(sf.Vars)

	// Validate tags: required and unique.
	entityTagOffset := dimensions + 1 // reserves entity_id plus position fields
	seenTags := make(map[int]string, len(keys))
	for _, k := range keys {
		def := sf.Vars[k]
		if def.Tag == 0 {
			log.Fatalf("entity %q: var %q: tag is required", sf.Entity, k)
		}
		if def.Tag+entityTagOffset == 1000 {
			log.Fatalf("entity %q: var %q: tag %d maps to reserved revision proto field 1000", sf.Entity, k, def.Tag)
		}
		if prev, ok := seenTags[def.Tag]; ok {
			log.Fatalf("entity %q: vars %q and %q share tag %d", sf.Entity, prev, k, def.Tag)
		}
		seenTags[def.Tag] = k
	}

	// Process vars in tag order for stable, tag-ordered output.
	slices.SortFunc(keys, func(a, b string) int { return sf.Vars[a].Tag - sf.Vars[b].Tag })

	tickBit := dimensions // 2D: 0=posX, 1=posY; 3D additionally reserves 2=posZ

	for _, k := range keys {
		def := sf.Vars[k]
		sync := def.Sync
		if sync == "" {
			sync = "tick"
		}

		var vi VarInfo
		vi.SnakeName = k
		vi.GoName = SnakeToPascal(k)
		vi.FieldName = SnakeToCamel(k)
		vi.TSPublicName = SnakeToCamel(k)
		vi.TSPrivateName = "_" + SnakeToCamel(k)
		vi.TSProtoField = SnakeToCamel(k)
		vi.CSPublicName = SnakeToPascal(k)
		vi.CSPrivateName = "_" + SnakeToCamel(k)
		vi.CSProtoField = SnakeToPascal(k)
		vi.UserTag = def.Tag
		vi.ProtoTag = def.Tag + entityTagOffset
		vi.Sync = sync

		isRepeated, isMap, mapKeyType, elemType, isCollection := ParseCollectionType(def.Type)
		if isCollection {
			vi.IsRepeated = isRepeated
			vi.IsMap = isMap
			vi.ElemProtoType = elemType
			if isMap {
				vi.MapKeyProtoType = mapKeyType
				if _, ok := protoToGo[mapKeyType]; !ok {
					log.Fatalf("entity %q: var %q: unknown map key type %q", sf.Entity, k, mapKeyType)
				}
			}

			// Resolve element type: custom type or scalar
			if _, isScalar := protoToGo[elemType]; isScalar {
				vi.ElemIsCustom = false
				if isRepeated {
					goElem := protoToGo[elemType]
					csElem := protoToCS[elemType]
					vi.GoType = "[]" + goElem
					vi.ElemGoType = goElem
					vi.TSType = protoToTS[elemType] + "[]"
					vi.ElemTSType = protoToTS[elemType]
					vi.CSType = "List<" + csElem + ">"
					vi.ElemCSType = csElem
				} else {
					goKey := protoToGo[mapKeyType]
					goVal := protoToGo[elemType]
					csKey := protoToCS[mapKeyType]
					csVal := protoToCS[elemType]
					vi.GoType = "map[" + goKey + "]" + goVal
					vi.ElemGoType = goVal
					vi.MapKeyGoType = goKey
					tsKey := protoToTS[mapKeyType]
					tsVal := protoToTS[elemType]
					vi.TSType = "Record<" + tsKey + ", " + tsVal + ">"
					vi.ElemTSType = tsVal
					vi.MapKeyTSType = tsKey
					vi.CSType = "Dictionary<" + csKey + ", " + csVal + ">"
					vi.ElemCSType = csVal
					vi.MapKeyCSType = csKey
				}
			} else {
				ct, ok := customTypes[elemType]
				if !ok {
					log.Fatalf("entity %q: var %q: unknown element type %q (not a scalar or custom type)", sf.Entity, k, elemType)
				}
				vi.ElemIsCustom = true
				usedTypes[elemType] = ct
				if isRepeated {
					vi.GoType = "[]" + elemType
					vi.ElemGoType = elemType
					vi.TSType = elemType + "[]"
					vi.ElemTSType = elemType
					vi.CSType = "List<" + elemType + ">"
					vi.ElemCSType = elemType
				} else {
					goKey := protoToGo[mapKeyType]
					tsKey := protoToTS[mapKeyType]
					csKey := protoToCS[mapKeyType]
					vi.GoType = "map[" + goKey + "]" + elemType
					vi.ElemGoType = elemType
					vi.MapKeyGoType = goKey
					vi.TSType = "Record<" + tsKey + ", " + elemType + ">"
					vi.ElemTSType = elemType
					vi.MapKeyTSType = tsKey
					vi.CSType = "Dictionary<" + csKey + ", " + elemType + ">"
					vi.ElemCSType = elemType
					vi.MapKeyCSType = csKey
				}
			}
			vi.TSDefault = "[]"
			vi.CSDefault = "new " + vi.CSType + "()"
			if isMap {
				vi.TSDefault = "{}"
			}
		} else {
			goType, ok := protoToGo[def.Type]
			if !ok {
				log.Fatalf("unknown proto type %q for var %q in entity %q", def.Type, k, sf.Entity)
			}
			tsType := protoToTS[def.Type]
			csType := protoToCS[def.Type]
			vi.GoType = goType
			vi.ProtoType = def.Type
			vi.ProtoHelper = protoToProtoHelper[def.Type]
			vi.TSType = tsType
			vi.TSDefault = tsDefaults[tsType]
			vi.CSType = csType
			vi.CSDefault = csDefaults[csType]
		}
		if _, ok := customTypes[vi.TSType]; ok {
			customTypeNames[vi.TSType] = true
		}
		if _, ok := customTypes[vi.ElemTSType]; ok {
			customTypeNames[vi.ElemTSType] = true
		}

		ed.AllVars = append(ed.AllVars, vi)
		if sync == "tick" {
			vi.BitIndex = tickBit
			tickBit++
			ed.TickVars = append(ed.TickVars, vi)
		} else {
			ed.OnceVars = append(ed.OnceVars, vi)
		}
	}

	// Collect used custom types in sorted order for stable output.
	for _, name := range SortedKeys(usedTypes) {
		ed.UsedCustomTypes = append(ed.UsedCustomTypes, usedTypes[name])
	}
	for _, name := range SortedKeys(customTypeNames) {
		ed.CustomTypeNames = append(ed.CustomTypeNames, name)
	}

	return ed
}

// LoadCommands reads all .yaml files from the command schemas directory and returns
// parsed commands. customTypes maps type name to resolved CustomTypeData so
// that command fields may reference custom composite types. Returns an empty
// slice (not an error) if the directory does not exist, so commands are optional.
// LoadCommands reads all .yaml files from the commands directory and returns
// parsed commands. Returns an empty slice (not an error) if the directory
// does not exist, so commands are optional. Returns an error if a file has an
// empty command name or if two files declare the same command name.
func LoadCommands(commandsDir string, customTypes map[string]CustomTypeData) ([]CommandData, error) {
	entries, err := os.ReadDir(commandsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading commands dir %s: %w", commandsDir, err)
	}

	var commands []CommandData
	seen := make(map[string]string) // command name -> first YAML filename
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(commandsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var cf CommandSchemaFile
		if err := yaml.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		name := strings.TrimSpace(cf.Command)
		if name == "" {
			return nil, fmt.Errorf("%s: command schema must set a non-empty command name", e.Name())
		}
		if prev, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate command name %q in %s and %s", name, prev, e.Name())
		}
		seen[name] = e.Name()
		commands = append(commands, BuildCommandData(cf, customTypes))
	}
	return commands, nil
}

// BuildCommandData converts a parsed command schema file into template-ready data.
// Fields are sorted alphabetically and assigned proto field numbers starting from 1.
// For entity-targeted commands an offset of 1 is added to reserve proto field 1 for entity_id.
// customTypes maps type name to resolved CustomTypeData so that command fields
// may reference custom composite types in addition to proto scalars.
func BuildCommandData(cf CommandSchemaFile, customTypes map[string]CustomTypeData) CommandData {
	cd := CommandData{
		Name:       cf.Command,
		LowerName:  LcFirst(cf.Command),
		Target:     cf.Target,
		EntityType: cf.EntityType,
	}
	if cd.Target == "" {
		cd.Target = "session"
	}

	// offset: entity-targeted commands reserve proto field 1 for entity_id.
	tagOffset := 0
	if cd.Target == "entity" {
		tagOffset = 1
	}

	keys := SortedKeys(cf.Fields)

	// Sort alphabetically for stable, deterministic proto field assignment.
	slices.SortFunc(keys, func(a, b string) int { return strings.Compare(a, b) })

	for i, k := range keys {
		def := cf.Fields[k]

		// Collection types are not supported on commands; reject with a clear message.
		if _, _, _, _, isCollection := ParseCollectionType(def.Type); isCollection {
			log.Fatalf("command %q: field %q: collection types (list/dict) are not supported on commands; use a flat custom type", cf.Command, k)
		}

		var fi CommandFieldInfo
		fi.SnakeName = k
		fi.GoName = SnakeToPascal(k)
		fi.FieldName = SnakeToCamel(k)
		fi.ProtoTag = (i + 1) + tagOffset

		if goType, ok := protoToGo[def.Type]; ok {
			// Proto scalar type.
			tsType := protoToTS[def.Type]
			csType := protoToCS[def.Type]
			fi.GoType = goType
			fi.ProtoType = def.Type
			fi.TSType = tsType
			fi.TSDefault = tsDefaults[tsType]
			fi.CSType = csType
			fi.CSDefault = csDefaults[csType]
		} else if ct, ok := customTypes[def.Type]; ok {
			// Custom composite type. TSDefault is an inline TS object literal built
			// from the custom type's scalar fields. This assumes custom type fields
			// are scalars only (enforced by BuildCustomTypeData); if nested custom
			// types are ever allowed, TSDefault construction would need to be recursive.
			parts := make([]string, 0, len(ct.Fields))
			for _, cf := range ct.Fields {
				parts = append(parts, cf.FieldName+": "+tsDefaults[cf.TSType])
			}
			fi.GoType = def.Type
			fi.ProtoType = def.Type
			fi.TSType = def.Type
			fi.TSDefault = "{ " + strings.Join(parts, ", ") + " }"
			fi.CSType = def.Type
			fi.CSDefault = "new " + def.Type + "()"
			fi.IsCustom = true
		} else {
			log.Fatalf("unknown type %q for field %q in command %q", def.Type, k, cf.Command)
		}

		cd.Fields = append(cd.Fields, fi)
	}
	return cd
}

// ValidateCommands checks that entity-targeted commands reference a known entity type.
func ValidateCommands(commands []CommandData, entities []EntityData) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	for _, c := range commands {
		if c.Target != "entity" {
			continue
		}
		if c.EntityType == "" {
			return fmt.Errorf("command %q targets an entity but has no entity_type", c.Name)
		}
		if !entityNames[c.EntityType] {
			return fmt.Errorf("command %q references unknown entity_type %q", c.Name, c.EntityType)
		}
	}
	return nil
}

// EventSchemaFile is the YAML model for one server event (e.g. schemas/events/chat_message.yaml).
type EventSchemaFile struct {
	Event      string                   `yaml:"event"`
	Target     string                   `yaml:"target"`      // required: "global", "session", or "entity"
	EntityType string                   `yaml:"entity_type"` // PascalCase entity name; required when target is "entity"
	FOIOnly    bool                     `yaml:"foi_only"`    // deliver only to sessions with entity in FOI; requires target "entity"
	Fields     map[string]EventFieldDef `yaml:"fields"`
}

// EventFieldDef defines a single field in an event schema.
// Supports scalar shorthand (`damage: int32`) and explicit (`damage: { type: int32 }`) YAML syntax.
type EventFieldDef struct {
	Type string `yaml:"type"`
}

// UnmarshalYAML allows scalar shorthand (`damage: int32`) as well as the explicit
// object form (`damage: { type: int32 }`).
func (e *EventFieldDef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		e.Type = value.Value
		return nil
	}
	type plain EventFieldDef
	return value.Decode((*plain)(e))
}

// EventFieldInfo holds computed metadata for a single event field.
type EventFieldInfo struct {
	SnakeName string
	GoName    string
	FieldName string // camelCase
	GoType    string
	ProtoType string
	TSType    string
	TSDefault string
	CSType    string
	CSDefault string
	IsCustom  bool // true when the field type is a custom composite type
	ProtoTag  int
}

// EventData is the fully-resolved template data for one server event type.
type EventData struct {
	Name       string
	LowerName  string
	Target     string // "global", "session", or "entity"
	EntityType string // PascalCase entity name; set when Target is "entity"
	FOIOnly    bool   // only valid when Target is "entity"
	Fields     []EventFieldInfo
}

// ServerEventField is one oneof entry in the generated ServerEvent message.
type ServerEventField struct {
	MessageType string // e.g. "ChatMessageEvent"
	SnakeName   string // e.g. "chat_message"
	Tag         int
}

// LoadEvents reads all .yaml files from the events directory and returns
// parsed events. Returns an empty slice (not an error) if the directory
// does not exist, so events are optional. Returns an error if a file has an
// empty event name or if two files declare the same event name.
func LoadEvents(eventsDir string, customTypes map[string]CustomTypeData) ([]EventData, error) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading events dir %s: %w", eventsDir, err)
	}

	var events []EventData
	seen := make(map[string]string) // event name -> first YAML filename
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(eventsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var ef EventSchemaFile
		if err := yaml.Unmarshal(data, &ef); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		name := strings.TrimSpace(ef.Event)
		if name == "" {
			return nil, fmt.Errorf("%s: event schema must set a non-empty event name", e.Name())
		}
		if prev, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate event name %q in %s and %s", name, prev, e.Name())
		}
		seen[name] = e.Name()
		events = append(events, BuildEventData(ef, customTypes))
	}
	return events, nil
}

// BuildEventData converts a parsed event schema file into template-ready data.
// Fields are sorted alphabetically and assigned proto field numbers starting from 1.
// For entity-targeted events an offset of 1 is added to reserve proto field 1 for entity_id.
func BuildEventData(ef EventSchemaFile, customTypes map[string]CustomTypeData) EventData {
	target := strings.TrimSpace(ef.Target)
	if target == "" {
		log.Fatalf("event %q: target is required (must be \"global\", \"session\", or \"entity\")", ef.Event)
	}
	ed := EventData{
		Name:       ef.Event,
		LowerName:  LcFirst(ef.Event),
		Target:     target,
		EntityType: ef.EntityType,
		FOIOnly:    ef.FOIOnly,
	}

	// offset: entity-targeted events reserve proto field 1 for entity_id.
	tagOffset := 0
	if target == "entity" {
		tagOffset = 1
	}

	keys := SortedKeys(ef.Fields)

	// Sort alphabetically for stable, deterministic proto field assignment.
	slices.SortFunc(keys, func(a, b string) int { return strings.Compare(a, b) })

	for i, k := range keys {
		def := ef.Fields[k]

		// Collection types are not supported on events; reject with a clear message.
		if _, _, _, _, isCollection := ParseCollectionType(def.Type); isCollection {
			log.Fatalf("event %q: field %q: collection types (list/dict) are not supported on events; use a flat custom type", ef.Event, k)
		}

		var fi EventFieldInfo
		fi.SnakeName = k
		fi.GoName = SnakeToPascal(k)
		fi.FieldName = SnakeToCamel(k)
		fi.ProtoTag = (i + 1) + tagOffset

		if goType, ok := protoToGo[def.Type]; ok {
			tsType := protoToTS[def.Type]
			csType := protoToCS[def.Type]
			fi.GoType = goType
			fi.ProtoType = def.Type
			fi.TSType = tsType
			fi.TSDefault = tsDefaults[tsType]
			fi.CSType = csType
			fi.CSDefault = csDefaults[csType]
		} else if ct, ok := customTypes[def.Type]; ok {
			parts := make([]string, 0, len(ct.Fields))
			for _, cf := range ct.Fields {
				parts = append(parts, cf.FieldName+": "+tsDefaults[cf.TSType])
			}
			fi.GoType = def.Type
			fi.ProtoType = def.Type
			fi.TSType = def.Type
			fi.TSDefault = "{ " + strings.Join(parts, ", ") + " }"
			fi.CSType = def.Type
			fi.CSDefault = "new " + def.Type + "()"
			fi.IsCustom = true
		} else {
			log.Fatalf("unknown type %q for field %q in event %q", def.Type, k, ef.Event)
		}

		ed.Fields = append(ed.Fields, fi)
	}
	return ed
}

// ValidateEvents checks event schemas for consistency: target must be a known
// value; entity_type and foi_only are only valid with target "entity"; entity_type
// is required when target is "entity" and must name a known entity.
func ValidateEvents(events []EventData, entities []EntityData) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	for _, ev := range events {
		switch ev.Target {
		case "global", "session", "entity":
			// valid
		default:
			return fmt.Errorf("event %q: target %q is invalid (must be \"global\", \"session\", or \"entity\")", ev.Name, ev.Target)
		}
		if ev.Target != "entity" {
			if ev.EntityType != "" {
				return fmt.Errorf("event %q: entity_type requires target \"entity\", got %q", ev.Name, ev.Target)
			}
			if ev.FOIOnly {
				return fmt.Errorf("event %q: foi_only requires target \"entity\", got %q", ev.Name, ev.Target)
			}
		} else {
			if ev.EntityType == "" {
				return fmt.Errorf("event %q: entity_type is required when target is \"entity\"", ev.Name)
			}
			if !entityNames[ev.EntityType] {
				return fmt.Errorf("event %q references unknown entity_type %q", ev.Name, ev.EntityType)
			}
		}
	}
	return nil
}

// LoadWorldSchemas reads all .yaml files from the world directory and returns
// parsed world types sorted alphabetically by type name (for stable proto tags).
// Returns an empty slice (not an error) if the directory does not exist, so
// world schemas are optional. Returns an error if a file has an empty world
// name or if two files declare the same world type.
func LoadWorldSchemas(worldDir string, customTypes map[string]CustomTypeData) ([]WorldTypeData, error) {
	entries, err := os.ReadDir(worldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading world dir %s: %w", worldDir, err)
	}

	var worldTypes []WorldTypeData
	seen := make(map[string]string) // world type name -> first YAML filename
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(worldDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var wf WorldSchemaFile
		if err := yaml.Unmarshal(data, &wf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		name := strings.TrimSpace(wf.World)
		if name == "" {
			return nil, fmt.Errorf("%s: world schema must set a non-empty world type name", e.Name())
		}
		if prev, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate world type %q in %s and %s", name, prev, e.Name())
		}
		if err := validateWorldSource(e.Name(), wf.Source); err != nil {
			return nil, err
		}
		seen[name] = e.Name()
		wf.World = name
		worldTypes = append(worldTypes, BuildWorldTypeData(wf, customTypes))
	}
	slices.SortFunc(worldTypes, func(a, b WorldTypeData) int {
		return strings.Compare(a.Name, b.Name)
	})
	return worldTypes, nil
}

// validateWorldSource checks that a WorldSourceDef (if non-nil) has valid
// format and file fields.
func validateWorldSource(filename string, src *WorldSourceDef) error {
	if src == nil {
		return nil
	}
	if src.Format != "tiled" && src.Format != "ldtk" && src.Format != "catalog" {
		return fmt.Errorf("%s: source.format must be \"tiled\", \"ldtk\", or \"catalog\", got %q", filename, src.Format)
	}
	if strings.TrimSpace(src.File) == "" {
		return fmt.Errorf("%s: source.file must not be empty", filename)
	}
	if src.Format == "catalog" && strings.TrimSpace(src.Type) == "" {
		return fmt.Errorf("%s: catalog source must set a non-empty type", filename)
	}
	return nil
}

// BuildWorldTypeData converts a parsed world schema file into template-ready data.
// When the schema has a source: block, extracted properties are added as typed
// fields and a synthetic bytes tile_data field (or string map_url when url_prefix
// is set) is appended as the last field. For catalog sources, a synthetic
// collection field (list or dict of the declared custom type) is injected at tag 1.
func BuildWorldTypeData(wf WorldSchemaFile, customTypes map[string]CustomTypeData) WorldTypeData {
	dataName := wf.World + "Data"
	wt := WorldTypeData{
		Name:      wf.World,
		DataName:  dataName,
		SnakeName: ToSnakeCase(dataName),
		LowerName: LcFirst(wf.World),
		Source:    wf.Source,
	}

	// Catalog sources inject a single synthetic collection field and skip
	// the normal explicit-field processing.
	if wf.Source != nil && wf.Source.Format == "catalog" {
		wt.IsCatalog = true
		wt.CatalogKeyField = wf.Source.Key
		wt.CatalogItemType = wf.Source.Type

		ct, ok := customTypes[wf.Source.Type]
		if !ok {
			log.Fatalf("world %q: catalog source references unknown custom type %q", wf.World, wf.Source.Type)
		}

		fi := WorldFieldInfo{
			SnakeName:     "items",
			GoName:        "Items",
			FieldName:     "items",
			TSProtoField:  "items",
			CSProtoField:  "Items",
			ProtoTag:      1,
			ElemProtoType: wf.Source.Type,
			ElemIsCustom:  true,
			ElemGoType:    wf.Source.Type,
			ElemTSType:    wf.Source.Type,
			ElemCSType:    wf.Source.Type,
		}

		if wf.Source.Key == "" {
			// List mode
			fi.IsRepeated = true
			fi.GoType = "[]" + wf.Source.Type
			fi.TSType = wf.Source.Type + "[]"
			fi.TSDefault = "[]"
			fi.CSType = "List<" + wf.Source.Type + ">"
			fi.CSDefault = "new " + fi.CSType + "()"
		} else {
			// Dict mode: find the key field's type on the custom type
			var keyProtoType, keyGoType, keyTSType, keyCSType string
			for _, cf := range ct.Fields {
				if cf.SnakeName == wf.Source.Key {
					keyProtoType = cf.ProtoType
					keyGoType = cf.GoType
					keyTSType = cf.TSType
					keyCSType = cf.CSType
					break
				}
			}
			if keyProtoType == "" {
				log.Fatalf("world %q: catalog key %q not found on type %q", wf.World, wf.Source.Key, wf.Source.Type)
			}
			fi.IsMap = true
			fi.MapKeyProtoType = keyProtoType
			fi.MapKeyGoType = keyGoType
			fi.MapKeyTSType = keyTSType
			fi.MapKeyCSType = keyCSType
			fi.GoType = "map[" + keyGoType + "]" + wf.Source.Type
			fi.TSType = "Record<" + keyTSType + ", " + wf.Source.Type + ">"
			fi.TSDefault = "{}"
			fi.CSType = "Dictionary<" + keyCSType + ", " + wf.Source.Type + ">"
			fi.CSDefault = "new " + fi.CSType + "()"
			wt.CatalogKeyTSType = keyTSType
			wt.CatalogKeyCSType = keyCSType
		}

		wt.Fields = []WorldFieldInfo{fi}
		return wt
	}

	// Merge explicit fields and source-extracted fields into a single sorted set.
	type rawField struct {
		name string
		typ  string
	}
	var raw []rawField

	for k, def := range wf.Fields {
		raw = append(raw, rawField{k, def.Type})
	}
	if wf.Source != nil {
		for _, ex := range wf.Source.Extract {
			raw = append(raw, rawField{ex.Name, ex.Type})
		}
	}

	// Validate that no user field uses the reserved synthetic field name.
	syntheticName := "tile_data"
	if wf.Source != nil && wf.Source.URLPrefix != "" {
		syntheticName = "map_url"
	}
	for _, rf := range raw {
		if rf.name == syntheticName {
			log.Fatalf("world %q: field name %q is reserved when source: is set", wf.World, syntheticName)
		}
	}

	// Sort for stable proto tags.
	slices.SortFunc(raw, func(a, b rawField) int {
		return strings.Compare(a.name, b.name)
	})

	// Deduplicate: fields from wf.Fields and source.Extract may overlap only if
	// they have the same type; duplicate names with different types are fatal.
	seen := make(map[string]string)
	protoTag := 1
	for _, rf := range raw {
		if prevType, ok := seen[rf.name]; ok {
			if prevType != rf.typ {
				log.Fatalf("world %q: field %q declared twice with conflicting types %q and %q", wf.World, rf.name, prevType, rf.typ)
			}
			continue // exact duplicate — skip
		}
		seen[rf.name] = rf.typ

		// Check for collection types first.
		isRepeated, isMap, mapKeyType, elemType, isCollection := ParseCollectionType(rf.typ)
		if isCollection {
			fi := WorldFieldInfo{
				SnakeName:     rf.name,
				GoName:        SnakeToPascal(rf.name),
				FieldName:     SnakeToCamel(rf.name),
				TSProtoField:  SnakeToCamel(rf.name),
				CSProtoField:  SnakeToPascal(rf.name),
				ProtoTag:      protoTag,
				IsRepeated:    isRepeated,
				IsMap:         isMap,
				ElemProtoType: elemType,
			}
			if isMap {
				fi.MapKeyProtoType = mapKeyType
				goKey, ok := protoToGo[mapKeyType]
				if !ok {
					log.Fatalf("world %q: field %q: unknown map key type %q", wf.World, rf.name, mapKeyType)
				}
				fi.MapKeyGoType = goKey
				fi.MapKeyTSType = protoToTS[mapKeyType]
				fi.MapKeyCSType = protoToCS[mapKeyType]
			}
			if ct, ok := customTypes[elemType]; ok {
				fi.ElemIsCustom = true
				fi.ElemGoType = elemType
				fi.ElemTSType = elemType
				fi.ElemCSType = elemType
				_ = ct
			} else if goElem, ok := protoToGo[elemType]; ok {
				fi.ElemGoType = goElem
				fi.ElemTSType = protoToTS[elemType]
				fi.ElemCSType = protoToCS[elemType]
			} else {
				log.Fatalf("world %q: field %q: unknown element type %q", wf.World, rf.name, elemType)
			}
			if isRepeated {
				fi.GoType = "[]" + fi.ElemGoType
				fi.TSType = fi.ElemTSType + "[]"
				fi.TSDefault = "[]"
				fi.CSType = "List<" + fi.ElemCSType + ">"
				fi.CSDefault = "new " + fi.CSType + "()"
			} else {
				fi.GoType = "map[" + fi.MapKeyGoType + "]" + fi.ElemGoType
				fi.TSType = "Record<" + fi.MapKeyTSType + ", " + fi.ElemTSType + ">"
				fi.TSDefault = "{}"
				fi.CSType = "Dictionary<" + fi.MapKeyCSType + ", " + fi.ElemCSType + ">"
				fi.CSDefault = "new " + fi.CSType + "()"
			}
			protoTag++
			wt.Fields = append(wt.Fields, fi)
			continue
		}

		goType, ok := protoToGo[rf.typ]
		if !ok {
			log.Fatalf("unknown proto type %q for field %q in world %q", rf.typ, rf.name, wf.World)
		}
		tsType := protoToTS[rf.typ]
		csType := protoToCS[rf.typ]

		fi := WorldFieldInfo{
			SnakeName:    rf.name,
			GoName:       SnakeToPascal(rf.name),
			FieldName:    SnakeToCamel(rf.name),
			GoType:       goType,
			ProtoType:    rf.typ,
			TSType:       tsType,
			TSDefault:    tsDefaults[tsType],
			TSProtoField: SnakeToCamel(rf.name),
			CSType:       csType,
			CSDefault:    csDefaults[csType],
			CSProtoField: SnakeToPascal(rf.name),
			ProtoTag:     protoTag,
		}
		protoTag++
		wt.Fields = append(wt.Fields, fi)
	}

	// Append the synthetic map-data field when a tiled/ldtk source is declared.
	if wf.Source != nil {
		var synType, synGoType, synTSType, synCSType string
		if wf.Source.URLPrefix != "" {
			synType = "string"
			synGoType = protoToGo["string"]
			synTSType = protoToTS["string"]
			synCSType = protoToCS["string"]
		} else {
			synType = "bytes"
			synGoType = protoToGo["bytes"]
			synTSType = protoToTS["bytes"]
			synCSType = protoToCS["bytes"]
		}
		wt.Fields = append(wt.Fields, WorldFieldInfo{
			SnakeName:    syntheticName,
			GoName:       SnakeToPascal(syntheticName),
			FieldName:    SnakeToCamel(syntheticName),
			GoType:       synGoType,
			ProtoType:    synType,
			TSType:       synTSType,
			TSDefault:    tsDefaults[synTSType],
			TSProtoField: SnakeToCamel(syntheticName),
			CSType:       synCSType,
			CSDefault:    csDefaults[synCSType],
			CSProtoField: SnakeToPascal(syntheticName),
			ProtoTag:     protoTag,
		})
	}

	return wt
}

// ValidateCatalogFile reads the catalog YAML at path, parses it as a sequence
// of entry maps, and if keyField is non-empty, verifies that all key values are
// present and unique. Returns an error on the first duplicate key.
func ValidateCatalogFile(path, keyField string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading catalog file: %w", err)
	}
	var entries []map[string]any
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parsing catalog file: %w", err)
	}
	if keyField == "" {
		return nil
	}
	seen := make(map[any]int)
	for i, entry := range entries {
		val, ok := entry[keyField]
		if !ok {
			return fmt.Errorf("entry %d is missing key field %q", i, keyField)
		}
		if prev, exists := seen[val]; exists {
			return fmt.Errorf("duplicate key %v at entries %d and %d (field %q)", val, prev, i, keyField)
		}
		seen[val] = i
	}
	return nil
}

// BuildProtoData assembles the template data for entities.proto from entities,
// commands, world types, config, custom types, and events.
func BuildProtoData(cfg *Config, entities []EntityData, commands []CommandData, worldTypes []WorldTypeData, customTypes []CustomTypeData, events []EventData) ProtoTemplateData {
	pd := ProtoTemplateData{
		Package:     cfg.Proto.Package,
		GoPackage:   cfg.Proto.GoPackage,
		Dimensions:  cfg.Simulation.Dimensions,
		Is3D:        cfg.Simulation.Dimensions == 3,
		Entities:    entities,
		Commands:    commands,
		CustomTypes: customTypes,
	}

	tag := 1
	for _, e := range entities {
		pd.EntityUpdateFields = append(pd.EntityUpdateFields, EntityUpdateField{
			MessageType: e.Name + "State",
			SnakeName:   ToSnakeCase(e.Name) + "_state",
			Tag:         tag,
		})
		tag++
		pd.EntityUpdateFields = append(pd.EntityUpdateFields, EntityUpdateField{
			MessageType: e.Name + "Delta",
			SnakeName:   ToSnakeCase(e.Name) + "_delta",
			Tag:         tag,
		})
		tag++
	}
	pd.EntityRemovedTag = tag

	cmdTag := 1
	for _, c := range commands {
		pd.ClientMessageFields = append(pd.ClientMessageFields, ClientMessageField{
			MessageType: c.Name + "Command",
			SnakeName:   ToSnakeCase(c.Name),
			Tag:         cmdTag,
		})
		cmdTag++
	}

	pd.WorldTypes = worldTypes
	worldTag := 1
	for _, wt := range worldTypes {
		pd.WorldUpdateFields = append(pd.WorldUpdateFields, WorldUpdateField{
			MessageType: wt.DataName,
			SnakeName:   wt.SnakeName,
			Tag:         worldTag,
		})
		worldTag++
	}

	pd.Events = events
	evtTag := 1
	for _, ev := range events {
		pd.ServerEventFields = append(pd.ServerEventFields, ServerEventField{
			MessageType: ev.Name + "Event",
			SnakeName:   ToSnakeCase(ev.Name),
			Tag:         evtTag,
		})
		evtTag++
	}

	return pd
}
