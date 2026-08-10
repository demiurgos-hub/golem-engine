package schema

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var protocolConstantNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// ProtocolConstantsFile is the YAML model for compile-time protocol string sets.
type ProtocolConstantsFile struct {
	Sets map[string]map[string]string `yaml:"sets"`
}

// ProtocolConstantSetData is one named set of generated protocol constants.
type ProtocolConstantSetData struct {
	SnakeName string
	Name      string
	Members   []ProtocolConstantData
}

// ProtocolConstantData is one generated protocol constant and its wire value.
type ProtocolConstantData struct {
	SnakeName string
	Name      string
	Value     string
}

// LoadProtocolConstants reads and validates an optional protocol constants catalog.
func LoadProtocolConstants(path string) ([]ProtocolConstantSetData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading protocol constants %s: %w", path, err)
	}

	var file ProtocolConstantsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing protocol constants %s: %w", path, err)
	}

	sets := make([]ProtocolConstantSetData, 0, len(file.Sets))
	seenSetNames := make(map[string]string, len(file.Sets))
	for _, setKey := range SortedKeys(file.Sets) {
		if !protocolConstantNamePattern.MatchString(setKey) {
			return nil, fmt.Errorf("protocol constant set %q must be canonical snake_case", setKey)
		}
		setName := SnakeToPascal(setKey)
		if previous, exists := seenSetNames[setName]; exists {
			return nil, fmt.Errorf("protocol constant sets %q and %q both generate %q", previous, setKey, setName)
		}
		seenSetNames[setName] = setKey

		members := file.Sets[setKey]
		if len(members) == 0 {
			return nil, fmt.Errorf("protocol constant set %q must contain at least one member", setKey)
		}
		set := ProtocolConstantSetData{SnakeName: setKey, Name: setName}
		seenMemberNames := make(map[string]string, len(members))
		seenValues := make(map[string]string, len(members))
		for _, memberKey := range SortedKeys(members) {
			if !protocolConstantNamePattern.MatchString(memberKey) {
				return nil, fmt.Errorf("protocol constant %q.%q must use canonical snake_case", setKey, memberKey)
			}
			memberName := SnakeToPascal(memberKey)
			if previous, exists := seenMemberNames[memberName]; exists {
				return nil, fmt.Errorf("protocol constants %q.%q and %q.%q both generate %q", setKey, previous, setKey, memberKey, memberName)
			}
			seenMemberNames[memberName] = memberKey

			value := members[memberKey]
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("protocol constant %q.%q must have a non-empty value", setKey, memberKey)
			}
			if value != strings.TrimSpace(value) {
				return nil, fmt.Errorf("protocol constant %q.%q value must not have surrounding whitespace", setKey, memberKey)
			}
			if previous, exists := seenValues[value]; exists {
				return nil, fmt.Errorf("protocol constants %q.%q and %q.%q duplicate value %q", setKey, previous, setKey, memberKey, value)
			}
			seenValues[value] = memberKey

			set.Members = append(set.Members, ProtocolConstantData{
				SnakeName: memberKey,
				Name:      memberName,
				Value:     value,
			})
		}
		sets = append(sets, set)
	}
	return sets, nil
}
