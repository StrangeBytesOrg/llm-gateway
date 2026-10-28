package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// RequestParams holds operator-enforced upstream request fields. Values
// stay as JSON so forwarding and persisted route snapshots preserve numbers.
type RequestParams map[string]json.RawMessage

var requestReservedFields = []string{
	"model", "models", "route", "messages", "prompt", "user", "stream",
	"stream_options", "n", "max_tokens", "max_completion_tokens",
}

func (s RequestParams) Validate() error {
	for field, value := range s {
		if slices.Contains(requestReservedFields, field) {
			return fmt.Errorf("field %q is controlled by the gateway", field)
		}
		if !json.Valid(value) {
			return fmt.Errorf("field %q must contain a JSON value", field)
		}
	}
	return nil
}

func (s *RequestParams) UnmarshalYAML(node *yaml.Node) error {
	// Retain the YAML decoder's alias expansion limits before traversing nodes.
	var decoded any
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	value, err := requestJSONValue(node, map[*yaml.Node]bool{})
	if err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("request settings must be an object")
	}
	settings := RequestParams{}
	for field, value := range object {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("request settings field %q: %w", field, err)
		}
		settings[field] = encoded
	}
	*s = settings
	return nil
}

// Convert explicitly instead of decoding into interface{}: YAML permits
// non-string keys, timestamps, custom tags, and recursive aliases that JSON does
// not. Duplicate keys must also be rejected inside flexible extension objects.
func requestJSONValue(node *yaml.Node, visiting map[*yaml.Node]bool) (any, error) {
	if node == nil || visiting[node] {
		return nil, errors.New("request settings contains a recursive YAML alias")
	}
	visiting[node] = true
	defer delete(visiting, node)
	switch node.Kind {
	case yaml.AliasNode:
		return requestJSONValue(node.Alias, visiting)
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			break
		}
		object := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, errors.New("request settings object keys must be strings")
			}
			if _, exists := object[key.Value]; exists {
				return nil, fmt.Errorf("request settings key %q already defined", key.Value)
			}
			value, err := requestJSONValue(node.Content[i+1], visiting)
			if err != nil {
				return nil, fmt.Errorf("request settings field %q: %w", key.Value, err)
			}
			object[key.Value] = value
		}
		return object, nil
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			break
		}
		array := make([]any, 0, len(node.Content))
		for _, item := range node.Content {
			value, err := requestJSONValue(item, visiting)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		return array, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!bool", "!!int", "!!float", "!!null":
			var value any
			if err := node.Decode(&value); err != nil {
				return nil, err
			}
			if _, err := json.Marshal(value); err != nil {
				return nil, fmt.Errorf("request settings value is not JSON-compatible: %w", err)
			}
			return value, nil
		}
	}
	return nil, fmt.Errorf("request settings value with YAML tag %q is not JSON-compatible", node.Tag)
}

func validateRequestParamsNodes(document *yaml.Node) error {
	// Decode each level into nodes so YAML aliases and merge keys have the same
	// behavior as the normal configuration decoder. Leave invalid outer shapes
	// to that decoder so its existing error messages remain intact.
	var root map[string]yaml.Node
	if err := document.Decode(&root); err != nil {
		return nil
	}
	models, exists := root["models"]
	if !exists {
		return nil
	}
	var routes map[string]yaml.Node
	if err := models.Decode(&routes); err != nil {
		return nil
	}
	for name, route := range routes {
		var fields map[string]yaml.Node
		if err := route.Decode(&fields); err != nil {
			continue
		}
		settings, exists := fields["request_params"]
		if !exists {
			continue
		}
		var parsed RequestParams
		if err := parsed.UnmarshalYAML(&settings); err != nil {
			return fmt.Errorf("model %s request_params: %w", name, err)
		}
	}
	return nil
}
