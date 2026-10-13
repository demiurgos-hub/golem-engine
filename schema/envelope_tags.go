package schema

import "fmt"

const (
	maxProtoFieldNumber     = 1<<29 - 1
	reservedProtoFieldStart = 19000
	reservedProtoFieldEnd   = 19999
)

func optionalEnvelopeTag(tag *int) int {
	if tag == nil {
		return 0
	}
	return *tag
}

func validateExplicitEnvelopeTag(kind, name string, tag *int) error {
	if tag != nil && *tag < 1 {
		return fmt.Errorf("%s %q: envelope tag must be at least 1, got %d", kind, name, *tag)
	}
	return nil
}

// ValidateEnvelopeTags validates the generated protocol-envelope field numbers.
// Untagged declarations retain the legacy sequential assignment. Explicitly tagged
// declarations are additive and may not overlap those implicit fields.
func ValidateEnvelopeTags(entities []EntityData, commands []CommandData, events []EventData) error {
	return validateProtocolLayout(allocateProtocolLayout(entities, commands, events, nil))
}

type namedEnvelopeTag struct {
	name string
	tag  int
}

func commandEnvelopeTags(commands []CommandData) []namedEnvelopeTag {
	result := make([]namedEnvelopeTag, 0, len(commands))
	for _, command := range commands {
		result = append(result, namedEnvelopeTag{name: command.Name, tag: command.UpdateTag})
	}
	return result
}

func eventEnvelopeTags(events []EventData) []namedEnvelopeTag {
	result := make([]namedEnvelopeTag, 0, len(events))
	for _, event := range events {
		result = append(result, namedEnvelopeTag{name: event.Name, tag: event.UpdateTag})
	}
	return result
}

func validateProtocolLayout(layout ProtocolLayout) error {
	for _, envelope := range []EnvelopeLayout{layout.Entity, layout.Command, layout.Event} {
		occupied := make(map[int]string, len(envelope.Variants))
		for _, explicit := range []bool{false, true} {
			for _, v := range envelope.Variants {
				if v.Explicit != explicit {
					continue
				}
				if v.Role == "state" && explicit && v.Tag == maxProtoFieldNumber {
					return fmt.Errorf("EntityUpdate %q state tag %d leaves no valid delta tag", v.Declaration, v.Tag)
				}
				name := v.Declaration
				if envelope.Name == "EntityUpdate" {
					name = v.MessageType
				}
				if err := claimEnvelopeTag(occupied, envelope.Name, name, v.Tag); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func claimEnvelopeTag(occupied map[int]string, scope, name string, tag int) error {
	if tag < 1 || tag > maxProtoFieldNumber {
		return fmt.Errorf("%s %q has invalid envelope tag %d", scope, name, tag)
	}
	if tag >= reservedProtoFieldStart && tag <= reservedProtoFieldEnd {
		return fmt.Errorf("%s %q uses reserved protobuf tag %d", scope, name, tag)
	}
	if previous, exists := occupied[tag]; exists {
		return fmt.Errorf("%s envelope tag %d overlaps %q and %q", scope, tag, previous, name)
	}
	occupied[tag] = name
	return nil
}
