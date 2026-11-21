package schema

import "fmt"

// fixedProtoMessages are the envelope and transition messages every generated
// protocol may declare in the same package as schema-derived messages. They are
// claimed even when an envelope is omitted (for example ClientMessage without
// commands) so adding a schema family later cannot introduce a clash.
var fixedProtoMessages = []string{
	"EntityRemoved", "EntityUpdate", "ClientMessage", "ClientPacket", "WorldUpdate",
	"ServerEvent", "SpaceChanged", "SpaceSettled", "WorldCleared", "ServerMessage",
}

// validateProtoMessageNames rejects two generated top-level protobuf messages
// with one name: custom types, <Entity>State/<Entity>Delta, <Command>Command,
// world <Type>Data, <Event>Event and the fixed envelope messages share a
// package, and every client codec emits one type per message.
func validateProtoMessageNames(pd ProtoTemplateData) error {
	claimed := make(map[string]string, len(fixedProtoMessages)+len(pd.CustomTypes)+2*len(pd.Entities)+len(pd.Commands)+len(pd.WorldTypes)+len(pd.Events))
	claim := func(name, source string) error {
		if prev, exists := claimed[name]; exists {
			return fmt.Errorf("generated protocol message %q for %s clashes with %s", name, source, prev)
		}
		claimed[name] = source
		return nil
	}
	for _, name := range fixedProtoMessages {
		if err := claim(name, "the built-in "+name+" message"); err != nil {
			return err
		}
	}
	for _, typ := range pd.CustomTypes {
		if err := claim(typ.Name, "custom type "+typ.Name); err != nil {
			return err
		}
	}
	for _, entity := range pd.Entities {
		for _, name := range []string{entity.Name + "State", entity.Name + "Delta"} {
			if err := claim(name, "entity "+entity.Name); err != nil {
				return err
			}
		}
	}
	for _, command := range pd.Commands {
		if err := claim(command.Name+"Command", "command "+command.Name); err != nil {
			return err
		}
	}
	for _, world := range pd.WorldTypes {
		if err := claim(world.DataName, "world type "+world.Name); err != nil {
			return err
		}
	}
	for _, event := range pd.Events {
		if err := claim(event.Name+"Event", "event "+event.Name); err != nil {
			return err
		}
	}
	return nil
}
