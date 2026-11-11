package config

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// document is one file of the configuration as it is written: its values, and
// the YAML they were read from, which knows the line each one is on.
type document struct {
	// root is the top-level node of the file, or nil for a file with nothing
	// in it.
	root *yaml.Node
	// value is what the file holds, in the types a JSON document decodes to:
	// a section is a map[string]any and a list a []any. A file with nothing in
	// it holds an empty section.
	value any
}

// parseDocument reads YAML into a document.
//
// What YAML has and JSON does not is turned into text, so that the schema
// sees what the file says rather than what a YAML parser guesses: a key
// written as a number or as true is the name it spells, a date such as
// 2026-11-12 stays the text it is, rather than becoming a time a setting could
// only ever receive as some reformatted string, and .inf is the word it is.
func parseDocument(raw []byte) (*document, error) {
	var file yaml.Node
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if len(file.Content) == 0 {
		return &document{value: map[string]any{}}, nil
	}
	root := file.Content[0]
	asText(root, false)
	var value any
	if err := root.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		value = map[string]any{}
	}
	return &document{root: root, value: value}, nil
}

// asText marks as text every key, every value YAML would read as a date, and
// the infinities and not-a-number, which JSON has no way to write.
func asText(n *yaml.Node, key bool) {
	switch n.Kind {
	case yaml.ScalarNode:
		switch {
		case key && n.Tag != mergeTag, n.Tag == "!!timestamp":
			n.Tag = "!!str"
		case n.Tag == "!!float":
			if v := strings.ToLower(strings.TrimLeft(n.Value, "+-")); v == ".inf" || v == ".nan" {
				n.Tag = "!!str"
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			asText(n.Content[i], true)
			asText(n.Content[i+1], false)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			asText(c, false)
		}
	}
}

// mergeTag marks the << key, which copies another section's settings into
// this one.
const mergeTag = "!!merge"

// line returns the line the value at loc is written on: the line of its key
// when it has one, so that a section is found where its name is rather than
// at its first setting. Where loc leads somewhere the file does not have, it
// is the line of the deepest part that is there, and 0 for a file with
// nothing in it.
func (d *document) line(loc []string) int {
	if d.root == nil {
		return 0
	}
	n, line := d.root, d.root.Line
	for _, tok := range loc {
		n = follow(n)
		switch n.Kind {
		case yaml.MappingNode:
			k, v := lookup(n, tok)
			if k == nil {
				return line
			}
			n, line = v, k.Line
		case yaml.SequenceNode:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n.Content) {
				return line
			}
			n = n.Content[i]
			line = n.Line
		default:
			return line
		}
	}
	return line
}

// follow returns the node an alias stands for.
func follow(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// lookup finds a key in a section, and its value: one written there, or else
// one the section takes from another through <<, as YAML resolves it.
func lookup(m *yaml.Node, key string) (k, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if c := m.Content[i]; c.Tag != mergeTag && c.Value == key {
			return c, m.Content[i+1]
		}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Tag != mergeTag {
			continue
		}
		from := follow(m.Content[i+1])
		sources := []*yaml.Node{from}
		if from.Kind == yaml.SequenceNode {
			sources = from.Content
		}
		for _, s := range sources {
			if s = follow(s); s.Kind == yaml.MappingNode {
				if k, v := lookup(s, key); k != nil {
					return k, v
				}
			}
		}
	}
	return nil, nil
}

// at returns the value at loc, and whether there is one.
func (d *document) at(loc []string) (any, bool) {
	v := d.value
	for _, tok := range loc {
		switch x := v.(type) {
		case map[string]any:
			item, ok := x[tok]
			if !ok {
				return nil, false
			}
			v = item
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
