package toolruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	toolNamePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	propertyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	toolVersionPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`)
)

func validateDefinition(definition Definition) error {
	if !toolNamePattern.MatchString(definition.Name) {
		return errors.New("tool name must be a bounded snake_case identifier")
	}
	if !toolVersionPattern.MatchString(definition.Version) {
		return errors.New("tool version is invalid")
	}
	if strings.TrimSpace(definition.Description) == "" || len(definition.Description) > 512 {
		return errors.New("tool description is required and must be bounded")
	}
	if definition.InputSchema.Type != "object" {
		return errors.New("tool input schema must be an object")
	}
	if definition.InputSchema.Properties == nil {
		return errors.New("tool input schema properties must be explicit")
	}
	for name, property := range definition.InputSchema.Properties {
		if !propertyNamePattern.MatchString(name) {
			return fmt.Errorf("invalid property name %q", name)
		}
		switch property.Type {
		case "string", "integer", "boolean":
		default:
			return fmt.Errorf("unsupported property type %q", property.Type)
		}
		if property.MaxLength < 0 || property.MinLength < 0 || (property.MaxLength > 0 && property.MinLength > property.MaxLength) {
			return fmt.Errorf("invalid string bounds for %q", name)
		}
	}
	for _, required := range definition.InputSchema.Required {
		if _, ok := definition.InputSchema.Properties[required]; !ok {
			return fmt.Errorf("required property %q is not declared", required)
		}
	}
	if len(definition.AllowedIntents) == 0 {
		return errors.New("at least one allowed intent is required")
	}
	if strings.TrimSpace(definition.RequiredPermission) == "" {
		return errors.New("required permission is required")
	}
	switch definition.SideEffect {
	case SideEffectReadOnly, SideEffectInternalWrite, SideEffectExternalWrite:
	default:
		return errors.New("unsupported side effect class")
	}
	if definition.TimeoutMS < 10 || definition.TimeoutMS > 120000 {
		return errors.New("tool timeout must be between 10 and 120000 milliseconds")
	}
	if definition.MaxResultBytes < 128 || definition.MaxResultBytes > 1024*1024 {
		return errors.New("tool result limit must be between 128 bytes and 1 MiB")
	}
	if definition.RetryMaxAttempts < 1 || definition.RetryMaxAttempts > 3 {
		return errors.New("tool retry attempts must be between 1 and 3")
	}
	if !definition.Idempotent && (definition.RetryMaxAttempts > 1 || definition.CacheTTLMS > 0) {
		return errors.New("non-idempotent tools cannot retry or cache")
	}
	if definition.CacheTTLMS < 0 || definition.CacheTTLMS > 60000 {
		return errors.New("tool cache TTL must be between 0 and 60000 milliseconds")
	}
	if (definition.CircuitFailures == 0) != (definition.CircuitOpenMS == 0) {
		return errors.New("circuit threshold and open duration must be enabled together")
	}
	if definition.CircuitFailures < 0 || definition.CircuitFailures > 10 || definition.CircuitOpenMS < 0 || definition.CircuitOpenMS > 60000 {
		return errors.New("tool circuit policy is outside the allowed bounds")
	}
	return nil
}

func validateArguments(schema InputSchema, raw json.RawMessage) (map[string]any, []byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var arguments map[string]any
	if err := decoder.Decode(&arguments); err != nil {
		return nil, nil, errors.New("arguments must be one JSON object")
	}
	if arguments == nil {
		return nil, nil, errors.New("arguments must not be null")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, nil, err
	}
	for name := range arguments {
		if _, ok := schema.Properties[name]; !ok && !schema.AdditionalProperties {
			return nil, nil, fmt.Errorf("unknown argument %q", name)
		}
	}
	for _, name := range schema.Required {
		if _, ok := arguments[name]; !ok {
			return nil, nil, fmt.Errorf("required argument %q is missing", name)
		}
	}
	for name, value := range arguments {
		property, declared := schema.Properties[name]
		if !declared {
			continue
		}
		if err := validateProperty(name, property, value); err != nil {
			return nil, nil, err
		}
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return nil, nil, errors.New("arguments could not be canonicalized")
	}
	return arguments, canonical, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain exactly one JSON value")
	}
	return nil
}

func validateProperty(name string, property PropertySchema, value any) error {
	switch property.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("argument %q must be a string", name)
		}
		length := len([]rune(text))
		if length < property.MinLength || (property.MaxLength > 0 && length > property.MaxLength) {
			return fmt.Errorf("argument %q is outside the allowed length", name)
		}
		if len(property.Enum) > 0 && !contains(property.Enum, text) {
			return fmt.Errorf("argument %q is outside the allowed values", name)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("argument %q must be an integer", name)
		}
		if _, err := number.Int64(); err != nil {
			return fmt.Errorf("argument %q must be an integer", name)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("argument %q must be a boolean", name)
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
