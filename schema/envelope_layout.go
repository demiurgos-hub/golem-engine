package schema

// EnvelopeVariant is a resolved oneof field. Role identifies state/delta/removal
// variants without requiring integration consumers to interpret message names.
type EnvelopeVariant struct {
	MessageType, SnakeName string
	Tag                    int
	Role, Declaration      string
	Explicit               bool
}

// EnvelopeLayout holds variants in generation order, not sorted tag order.
type EnvelopeLayout struct {
	Name     string
	Variants []EnvelopeVariant
}

// ProtocolLayout holds resolved envelope layouts for all schema message families.
type ProtocolLayout struct{ Entity, Command, Event, World EnvelopeLayout }

func allocateEnvelope(name string, declarations []namedEnvelopeTag, suffix, role string, paired bool) EnvelopeLayout {
	layout := EnvelopeLayout{Name: name}
	tag := 1
	for _, explicit := range []bool{false, true} {
		for _, d := range declarations {
			if (d.tag != 0) != explicit {
				continue
			}
			n := tag
			if explicit {
				n = d.tag
			}
			snake := ToSnakeCase(d.name)
			if paired {
				snake += "_state"
			}
			layout.Variants = append(layout.Variants, EnvelopeVariant{d.name + suffix, snake, n, role, d.name, explicit})
			if paired {
				layout.Variants = append(layout.Variants, EnvelopeVariant{d.name + "Delta", ToSnakeCase(d.name) + "_delta", n + 1, "delta", d.name, explicit})
			}
			if !explicit {
				tag++
				if paired {
					tag++
				}
			}
		}
	}
	if paired {
		layout.Variants = append(layout.Variants, EnvelopeVariant{"EntityRemoved", "entity_removed", tag, "removal", "EntityRemoved", false})
	}
	return layout
}

func allocateProtocolLayout(entities []EntityData, commands []CommandData, events []EventData, worlds []WorldTypeData) ProtocolLayout {
	var declarations []namedEnvelopeTag
	for _, e := range entities {
		declarations = append(declarations, namedEnvelopeTag{e.Name, e.UpdateTag})
	}
	layout := ProtocolLayout{
		Entity:  allocateEnvelope("EntityUpdate", declarations, "State", "state", true),
		Command: allocateEnvelope("ClientMessage", commandEnvelopeTags(commands), "Command", "command", false),
		Event:   allocateEnvelope("ServerEvent", eventEnvelopeTags(events), "Event", "event", false),
		World:   EnvelopeLayout{Name: "WorldUpdate"},
	}
	for i, world := range worlds {
		tag := world.UpdateTag
		if tag < 1 {
			tag = i + 1
		}
		layout.World.Variants = append(layout.World.Variants, EnvelopeVariant{MessageType: world.DataName, SnakeName: world.SnakeName, Tag: tag, Role: "world", Declaration: world.Name})
	}
	return layout
}

// EnvelopeLayouts returns resolved layouts, adapting legacy hand-built proto DTOs.
// The adapter preserves authored variant tags; it does not allocate or validate.
func (pd ProtoTemplateData) EnvelopeLayouts() ProtocolLayout {
	if pd.Layout != nil {
		return *pd.Layout
	}
	l := ProtocolLayout{Entity: EnvelopeLayout{Name: "EntityUpdate"}, Command: EnvelopeLayout{Name: "ClientMessage"}, Event: EnvelopeLayout{Name: "ServerEvent"}, World: EnvelopeLayout{Name: "WorldUpdate"}}
	for _, f := range pd.EntityUpdateFields {
		l.Entity.Variants = append(l.Entity.Variants, EnvelopeVariant{MessageType: f.MessageType, SnakeName: f.SnakeName, Tag: f.Tag})
	}
	l.Entity.Variants = append(l.Entity.Variants, EnvelopeVariant{MessageType: "EntityRemoved", SnakeName: "entity_removed", Tag: pd.EntityRemovedTag, Role: "removal"})
	for _, f := range pd.ClientMessageFields {
		l.Command.Variants = append(l.Command.Variants, EnvelopeVariant{MessageType: f.MessageType, SnakeName: f.SnakeName, Tag: f.Tag})
	}
	for _, f := range pd.ServerEventFields {
		l.Event.Variants = append(l.Event.Variants, EnvelopeVariant{MessageType: f.MessageType, SnakeName: f.SnakeName, Tag: f.Tag})
	}
	for _, f := range pd.WorldUpdateFields {
		l.World.Variants = append(l.World.Variants, EnvelopeVariant{MessageType: f.MessageType, SnakeName: f.SnakeName, Tag: f.Tag})
	}
	return l
}

// ResolveProtoData builds and validates protocol metadata before any output is written.
// World validation remains with LoadWorldSchemas and keeps its separate policy.
func ResolveProtoData(cfg *Config, entities []EntityData, commands []CommandData, worlds []WorldTypeData, customTypes []CustomTypeData, events []EventData) (ProtoTemplateData, error) {
	pd := BuildProtoData(cfg, entities, commands, worlds, customTypes, events)
	if err := validateProtocolLayout(*pd.Layout); err != nil {
		return ProtoTemplateData{}, err
	}
	return pd, nil
}
