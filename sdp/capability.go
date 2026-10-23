package sdp

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var ErrNoCompatibleConfiguration = errors.New("no compatible SDP capability configuration")

type PotentialConfiguration struct {
	ID            uint32
	TransportRefs []uint32
	AttributeRefs []uint32
	Raw           string
}

type CapabilitySet struct {
	Supported  []string
	Required   []string
	Transports map[uint32][]string
	Attributes map[uint32]Attribute
	Potential  []PotentialConfiguration
	Actual     []PotentialConfiguration
}

type SelectedConfiguration struct {
	ID         uint32
	Transport  string
	Attributes []Attribute
}

func ParseCapabilities(attributes []Attribute) (CapabilitySet, error) {
	capabilities := CapabilitySet{
		Transports: make(map[uint32][]string),
		Attributes: make(map[uint32]Attribute),
	}

	for _, attribute := range attributes {
		switch strings.ToLower(attribute.Name) {
		case "csup":
			capabilities.Supported = append(capabilities.Supported, strings.Fields(attribute.Value)...)
		case "creq":
			capabilities.Required = append(capabilities.Required, strings.Fields(attribute.Value)...)
		case "tcap":
			id, values, err := parseCapabilityDefinition(attribute.Value)
			if err != nil {
				return CapabilitySet{}, fmt.Errorf("parse tcap: %w", err)
			}

			capabilities.Transports[id] = values
		case "acap":
			id, rawAttribute, err := parseAttributeCapability(attribute.Value)
			if err != nil {
				return CapabilitySet{}, fmt.Errorf("parse acap: %w", err)
			}

			capabilities.Attributes[id] = rawAttribute
		case "pcfg", "acfg":
			configuration, err := parsePotentialConfiguration(attribute.Value)
			if err != nil {
				return CapabilitySet{}, fmt.Errorf("parse %s: %w", attribute.Name, err)
			}

			if strings.EqualFold(attribute.Name, "pcfg") {
				capabilities.Potential = append(capabilities.Potential, configuration)
			} else {
				capabilities.Actual = append(capabilities.Actual, configuration)
			}
		}
	}

	sort.Slice(capabilities.Potential, func(first, second int) bool {
		return capabilities.Potential[first].ID < capabilities.Potential[second].ID
	})

	return capabilities, nil
}

func (c CapabilitySet) Select(
	supportedTransports map[string]bool,
	supportsAttribute func(Attribute) bool,
) (SelectedConfiguration, error) {
	for _, configuration := range c.Potential {
		selected := SelectedConfiguration{ID: configuration.ID}

		for _, reference := range configuration.TransportRefs {
			for _, transport := range c.Transports[reference] {
				if supportedTransports[strings.ToUpper(transport)] {
					selected.Transport = transport

					break
				}
			}

			if selected.Transport != "" {
				break
			}
		}

		if len(configuration.TransportRefs) > 0 && selected.Transport == "" {
			continue
		}

		compatible := true

		for _, reference := range configuration.AttributeRefs {
			attribute, exists := c.Attributes[reference]
			if !exists || (supportsAttribute != nil && !supportsAttribute(attribute)) {
				compatible = false

				break
			}

			selected.Attributes = append(selected.Attributes, attribute)
		}

		if compatible {
			return selected, nil
		}
	}

	return SelectedConfiguration{}, ErrNoCompatibleConfiguration
}

func parseCapabilityDefinition(value string) (uint32, []string, error) {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		return 0, nil, ErrInvalidDescription
	}

	id, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || id == 0 {
		return 0, nil, ErrInvalidDescription
	}

	return uint32(id), append([]string(nil), parts[1:]...), nil
}

func parseAttributeCapability(value string) (uint32, Attribute, error) {
	idText, rawAttribute, found := strings.Cut(strings.TrimSpace(value), " ")
	if !found {
		return 0, Attribute{}, ErrInvalidDescription
	}

	id, err := strconv.ParseUint(idText, 10, 32)
	if err != nil || id == 0 {
		return 0, Attribute{}, ErrInvalidDescription
	}

	attribute, err := parseGenericAttribute(rawAttribute)
	if err != nil {
		return 0, Attribute{}, err
	}

	return uint32(id), attribute, nil
}

func parsePotentialConfiguration(value string) (PotentialConfiguration, error) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return PotentialConfiguration{}, ErrInvalidDescription
	}

	id, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || id == 0 {
		return PotentialConfiguration{}, ErrInvalidDescription
	}

	configuration := PotentialConfiguration{ID: uint32(id), Raw: value}

	for _, parameter := range parts[1:] {
		name, rawReferences, found := strings.Cut(parameter, "=")
		if !found {
			continue
		}

		references, err := parseCapabilityReferences(rawReferences)
		if err != nil {
			return PotentialConfiguration{}, err
		}

		switch name {
		case "t":
			configuration.TransportRefs = append(configuration.TransportRefs, references...)
		case "a":
			configuration.AttributeRefs = append(configuration.AttributeRefs, references...)
		}
	}

	return configuration, nil
}

func parseCapabilityReferences(value string) ([]uint32, error) {
	value = strings.Trim(value, "[]")
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == '|'
	})

	references := make([]uint32, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimPrefix(strings.TrimSpace(part), "-")
		if part == "" {
			continue
		}

		reference, err := strconv.ParseUint(part, 10, 32)
		if err != nil || reference == 0 {
			return nil, ErrInvalidDescription
		}

		references = append(references, uint32(reference))
	}

	return references, nil
}
