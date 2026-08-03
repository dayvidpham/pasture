package preferences

import "gopkg.in/yaml.v3"

// mappingRoot returns the top-level mapping node of a document, creating an
// empty mapping when the document is blank. It normalizes root into a document
// node wrapping exactly one mapping so unrelated keys round-trip.
func mappingRoot(root *yaml.Node) *yaml.Node {
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = []*yaml.Node{mapping}
			return mapping
		}
		return mappingRoot(root.Content[0])
	}
	if root.Kind != yaml.MappingNode {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Content = nil
	}
	return root
}

// findMappingValue returns the value node for key in the document's top-level
// mapping, or nil when absent.
func findMappingValue(root *yaml.Node, key string) *yaml.Node {
	if root.Kind == 0 {
		return nil
	}
	mapping := mappingRoot(root)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMappingValue replaces (or appends) key's value in the top-level mapping,
// leaving every unrelated key in place.
func setMappingValue(root *yaml.Node, key string, value *yaml.Node) {
	mapping := mappingRoot(root)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, keyNode, value)
}
