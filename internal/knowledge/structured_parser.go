package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxStructuredDepth = 128

func parseStructuredDataBlocks(content []byte, requireJSON bool) ([]sourceBlock, error) {
	if requireJSON && !json.Valid(content) {
		return nil, fmt.Errorf("invalid JSON document")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	blocks := make([]sourceBlock, 0)
	for documentIndex := 0; ; documentIndex++ {
		root := new(yaml.Node)
		if err := decoder.Decode(root); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("invalid structured document: %w", err)
		}
		if len(root.Content) == 0 {
			continue
		}
		path := []string(nil)
		if documentIndex > 0 {
			path = []string{fmt.Sprintf("document[%d]", documentIndex)}
		}
		if err := appendStructuredNode(&blocks, root.Content[0], path, root.Content[0].Line, 0); err != nil {
			return nil, err
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("structured document has no indexable values")
	}
	return blocks, nil
}

func appendStructuredNode(blocks *[]sourceBlock, node *yaml.Node, path []string, keyLine int, depth int) error {
	if node == nil {
		return nil
	}
	if depth > maxStructuredDepth {
		return fmt.Errorf("structured document nesting exceeds %d levels", maxStructuredDepth)
	}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) > 0 {
			return appendStructuredNode(blocks, node.Content[0], path, keyLine, depth+1)
		}
	case yaml.MappingNode:
		if len(node.Content) == 0 {
			appendStructuredScalar(blocks, path, keyLine, node.Line, "{}")
			return nil
		}
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			segment := strings.TrimSpace(key.Value)
			if segment == "" {
				segment = fmt.Sprintf("key@L%d", max(key.Line, 1))
			}
			if err := appendStructuredNode(blocks, value, appendPath(path, segment), max(key.Line, 1), depth+1); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if len(node.Content) == 0 {
			appendStructuredScalar(blocks, path, keyLine, node.Line, "[]")
			return nil
		}
		for index, item := range node.Content {
			itemPath := appendPath(path, fmt.Sprintf("[%d]", index))
			if err := appendStructuredNode(blocks, item, itemPath, max(item.Line, keyLine), depth+1); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		if node.Alias == nil {
			return fmt.Errorf("structured document contains an unresolved alias")
		}
		return appendStructuredNode(blocks, node.Alias, path, keyLine, depth+1)
	case yaml.ScalarNode:
		appendStructuredScalar(blocks, path, keyLine, scalarEndLine(node), structuredScalarValue(node))
	default:
		return fmt.Errorf("unsupported structured node kind %d", node.Kind)
	}
	return nil
}

func appendStructuredScalar(blocks *[]sourceBlock, path []string, lineStart int, lineEnd int, value string) {
	section := structuredSection(path)
	content := structuredKeyPath(path) + " = " + value
	block := newSourceBlock(section, max(lineStart, 1), max(lineEnd, lineStart), content, false)
	block.lineAware = true
	*blocks = append(*blocks, block)
}

func appendPath(path []string, segment string) []string {
	result := make([]string, len(path), len(path)+1)
	copy(result, path)
	return append(result, segment)
}

func structuredSection(path []string) string {
	// Group scalar siblings under their containing object/sequence item. The
	// full leaf path remains in the content, while retrieving one field also
	// brings the nearby fields needed by multi-part configuration questions.
	if len(path) <= 1 {
		return "$"
	}
	return strings.Join(path[:len(path)-1], " > ")
}

func structuredKeyPath(path []string) string {
	if len(path) == 0 {
		return "$"
	}
	var builder strings.Builder
	for index, segment := range path {
		if strings.HasPrefix(segment, "[") {
			builder.WriteString(segment)
			continue
		}
		if index > 0 {
			builder.WriteByte('.')
		}
		builder.WriteString(segment)
	}
	return builder.String()
}

func structuredScalarValue(node *yaml.Node) string {
	if node.Tag == "!!str" {
		return strconv.Quote(node.Value)
	}
	if strings.TrimSpace(node.Value) == "" {
		return "null"
	}
	return node.Value
}

func scalarEndLine(node *yaml.Node) int {
	if node == nil {
		return 1
	}
	return max(node.Line+strings.Count(node.Value, "\n"), node.Line)
}
