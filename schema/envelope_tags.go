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
	if err := validateEntityEnvelopeTags(entities); err != nil {
		return err
	}
	if err := validateSingleEnvelopeTags("ClientMessage", commandEnvelopeTags(commands)); err != nil {
		return err
	}
	return validateSingleEnvelopeTags("ServerEvent", eventEnvelopeTags(events))
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

func validateEntityEnvelopeTags(entities []EntityData) error {
	occupied := make(map[int]string, len(entities)*2+1)
	implicitTag := 1
	for _, entity := range entities {
		if entity.UpdateTag != 0 {
			continue
		}
		if err := claimEnvelopeTag(occupied, "EntityUpdate", entity.Name+"State", implicitTag); err != nil {
			return err
		}
		if err := claimEnvelopeTag(occupied, "EntityUpdate", entity.Name+"Delta", implicitTag+1); err != nil {
			return err
		}
		implicitTag += 2
	}
	if err := claimEnvelopeTag(occupied, "EntityUpdate", "EntityRemoved", implicitTag); err != nil {
		return err
	}

	for _, entity := range entities {
		if entity.UpdateTag == 0 {
			continue
		}
		if entity.UpdateTag == maxProtoFieldNumber {
			return fmt.Errorf("EntityUpdate %q state tag %d leaves no valid delta tag", entity.Name, entity.UpdateTag)
		}
		if err := claimEnvelopeTag(occupied, "EntityUpdate", entity.Name+"State", entity.UpdateTag); err != nil {
			return err
		}
		if err := claimEnvelopeTag(occupied, "EntityUpdate", entity.Name+"Delta", entity.UpdateTag+1); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleEnvelopeTags(scope string, declarations []namedEnvelopeTag) error {
	occupied := make(map[int]string, len(declarations))
	implicitTag := 1
	for _, declaration := range declarations {
		if declaration.tag != 0 {
			continue
		}
		if err := claimEnvelopeTag(occupied, scope, declaration.name, implicitTag); err != nil {
			return err
		}
		implicitTag++
	}
	for _, declaration := range declarations {
		if declaration.tag == 0 {
			continue
		}
		if err := claimEnvelopeTag(occupied, scope, declaration.name, declaration.tag); err != nil {
			return err
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
