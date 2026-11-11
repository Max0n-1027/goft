// Package schema holds the JSON Schema of a goft configuration file.
//
// goft checks every file it reads against this schema before anything else,
// and an editor that understands JSON Schema can check the same file as it is
// written: the schema is published beside this file in the repository, is
// included in every release archive, and is printed by goft schema.
package schema

import _ "embed"

// JSON is the schema, in JSON Schema draft-07.
//
//go:embed goft.schema.json
var JSON string
