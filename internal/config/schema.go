package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"goft/internal/i18n"
	"goft/schema"
)

// schemaURL is the name the schema is compiled under. Nothing is fetched from
// it: the document is handed to the compiler from memory.
const schemaURL = "file:///goft.schema.json"

// Definitions in the schema whose failures are explained in their own terms.
const (
	envDef      = schemaURL + "#/definitions/env"
	durationDef = schemaURL + "#/definitions/duration"
)

// compiled is goft's schema, compiled once.
var compiled = sync.OnceValues(func() (*compiledSchema, error) {
	return compileSchema(schema.JSON)
})

// compiledSchema is a schema ready to validate with, and the document it was
// compiled from, which an error is looked up in to say what was expected.
type compiledSchema struct {
	schema *jsonschema.Schema
	doc    any
}

func compileSchema(src string) (*compiledSchema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(schemaURL)
	if err != nil {
		return nil, err
	}
	return &compiledSchema{schema: s, doc: doc}, nil
}

// problem is one way a file breaks the schema, and the line it is on.
type problem struct {
	line int
	err  error
}

// checkSchema checks a file against the schema and reports every way it
// breaks it, in the order they appear in the file.
func checkSchema(d *document) error {
	s, err := compiled()
	if err != nil {
		return i18n.Errorf("the configuration schema is broken: %w", err)
	}
	err = s.schema.Validate(d.value)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}

	r := reporter{doc: d, schema: s}
	for _, leaf := range leaves(ve) {
		r.explain(leaf)
	}
	// The schema library visits a section's settings in no fixed order, so
	// problems on one line are put in order too, to say the same every time.
	sort.Slice(r.problems, func(i, j int) bool {
		a, b := r.problems[i], r.problems[j]
		if a.line != b.line {
			return a.line < b.line
		}
		return a.err.Error() < b.err.Error()
	})

	var errs []error
	seen := map[string]bool{}
	for _, p := range r.problems {
		err := p.err
		if p.line > 0 {
			err = i18n.Errorf("line %d: %w", p.line, p.err)
		}
		if !seen[err.Error()] {
			seen[err.Error()] = true
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// leaves flattens an error into the failures worth reporting.
func leaves(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if _, ok := e.ErrorKind.(*kind.AnyOf); ok {
		return anyOfLeaves(e)
	}
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// anyOfLeaves picks, from a value that has none of the shapes a setting may
// take, the failures of the shape it was meant to have.
//
// Most settings also take ${NAME}, and a value that is not a reference fails
// that shape too; saying so would only distract. A shape the value does not
// even have the type of says less than one it has the type of but breaks in
// some detail, so type mismatches are reported only when every shape was one,
// and then together, as one "must be a or b".
func anyOfLeaves(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	var detail, mismatched []*jsonschema.ValidationError
	for _, shape := range e.Causes {
		if isEnv(shape) {
			continue
		}
		ls := leaves(shape)
		if len(ls) == 1 && isTypeMismatch(ls[0], e.InstanceLocation) {
			mismatched = append(mismatched, ls[0])
			continue
		}
		detail = append(detail, ls...)
	}
	if len(detail) > 0 || len(mismatched) == 0 {
		return detail
	}
	merged := &kind.Type{Got: mismatched[0].ErrorKind.(*kind.Type).Got}
	for _, m := range mismatched {
		merged.Want = append(merged.Want, m.ErrorKind.(*kind.Type).Want...)
	}
	return []*jsonschema.ValidationError{{
		SchemaURL:        e.SchemaURL,
		InstanceLocation: e.InstanceLocation,
		ErrorKind:        merged,
	}}
}

// isEnv reports whether a failure is the value not being a ${NAME} reference.
func isEnv(e *jsonschema.ValidationError) bool {
	if ref, ok := e.ErrorKind.(*kind.Reference); ok && ref.URL == envDef {
		return true
	}
	return e.SchemaURL == envDef
}

// isTypeMismatch reports whether a failure is only that the value at loc has
// the wrong type. A duration is not counted: it is a string with rules, and
// "must be a duration" says more than "must be a string".
func isTypeMismatch(e *jsonschema.ValidationError, loc []string) bool {
	_, ok := e.ErrorKind.(*kind.Type)
	return ok && !isDuration(e) && slices.Equal(e.InstanceLocation, loc)
}

// isDuration reports whether a failure is the value not being a duration,
// whichever of a duration's rules it broke.
func isDuration(e *jsonschema.ValidationError) bool {
	return e.SchemaURL == durationDef
}

// reporter turns the schema's failures into goft's messages: about a setting
// by the name it has in the file, under the job it belongs to, on a line.
type reporter struct {
	doc      *document
	schema   *compiledSchema
	problems []problem
}

// english renders a failure the schema library describes in its own words,
// which, like any library's, are not translated.
var english = message.NewPrinter(language.English)

func (r *reporter) explain(e *jsonschema.ValidationError) {
	loc := e.InstanceLocation
	if isDuration(e) {
		value, _ := r.doc.at(loc)
		r.add(loc, loc, i18n.N("%s must be a duration such as 30s, 5m or 1h30m, not %s"), show(value))
		return
	}
	switch k := e.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		choices := r.properties(e.SchemaURL)
		for _, name := range k.Properties {
			at := append(slices.Clip(loc), name)
			switch hint := suggest(name, choices); {
			case hint != "":
				r.add(at, at, i18n.N("%s is not a setting (did you mean %s?)"), hint)
			case len(choices) > 0 && len(choices) <= 4:
				r.add(at, at, i18n.N("%s is not a setting (%s)"), strings.Join(choices, " | "))
			default:
				r.add(at, at, i18n.N("%s is not a setting"))
			}
		}
	case *kind.Required:
		for _, name := range k.Missing {
			at := append(slices.Clip(loc), name)
			if cond := r.condition(e.SchemaURL, loc); !cond.IsZero() {
				r.add(loc, at, i18n.N("%s is required when %s"), cond)
			} else {
				r.add(loc, at, i18n.N("%s is required"))
			}
		}
	case *kind.FalseSchema:
		if cond := r.condition(e.SchemaURL, parent(loc)); !cond.IsZero() {
			r.add(loc, loc, i18n.N("%s is not used when %s"), cond)
		} else {
			r.add(loc, loc, i18n.N("%s is not a setting"))
		}
	case *kind.Type:
		want := typeNames(k.Want)
		if slices.Equal(k.Want, []string{"string"}) && k.Got != "object" && k.Got != "array" && k.Got != "null" {
			r.add(loc, loc, i18n.N("%s must be %s, not %s: put the value in quotes"), want, gotName(k.Got))
		} else {
			r.add(loc, loc, i18n.N("%s must be %s, not %s"), want, gotName(k.Got))
		}
	case *kind.Enum:
		allowed := make([]string, len(k.Want))
		for i, w := range k.Want {
			allowed[i] = fmt.Sprint(w)
		}
		if k.Got == nil {
			r.add(loc, loc, i18n.N("%s is empty (%s)"), strings.Join(allowed, " | "))
		} else {
			r.add(loc, loc, i18n.N("%s %s is invalid (%s)"), show(k.Got), strings.Join(allowed, " | "))
		}
	case *kind.Minimum:
		r.add(loc, loc, i18n.N("%s must be >= %s"), k.Want.RatString())
	case *kind.Maximum:
		r.add(loc, loc, i18n.N("%s must be <= %s"), k.Want.RatString())
	case *kind.MinLength:
		r.add(loc, loc, i18n.N("%s must not be empty"))
	default:
		r.add(loc, loc, i18n.N("%s: %s"), k.LocalizedString(english))
	}
}

// add records a problem with the setting at subject, on the line of the
// value at line. format takes the setting's name first, then args.
func (r *reporter) add(line, subject []string, format string, args ...any) {
	job, setting := r.name(subject)
	err := i18n.Errorf(format, append([]any{setting}, args...)...)
	if job != "" {
		err = i18n.Errorf("job %s: %w", job, err)
	}
	r.problems = append(r.problems, problem{line: r.doc.line(line), err: err})
}

// name says which setting loc is, the way goft's other checks do: a job's
// settings by their names within the job, with the job named in front, and
// everything else by its full name.
func (r *reporter) name(loc []string) (job string, setting any) {
	if len(loc) == 0 {
		return "", i18n.Msg("the file")
	}
	if len(loc) >= 2 && loc[0] == keyJobs {
		if i, err := strconv.Atoi(loc[1]); err == nil {
			job := "#" + strconv.Itoa(i+1)
			v, _ := r.doc.at(loc[:2])
			if m, ok := v.(map[string]any); ok {
				if n, ok := m["name"].(string); ok && strings.TrimSpace(n) != "" {
					job = n
				}
			}
			if len(loc) == 2 {
				return "", i18n.Msg("job %s", job)
			}
			return job, r.path(loc[:2], loc[2:])
		}
	}
	return "", r.path(nil, loc)
}

// path spells the setting at base+rel by its keys, relative to base: log.level,
// or include[2] for an item in a list.
func (r *reporter) path(base, rel []string) string {
	var b strings.Builder
	for i, tok := range rel {
		parent, _ := r.doc.at(append(slices.Clip(base), rel[:i]...))
		if _, list := parent.([]any); list {
			b.WriteString("[" + tok + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(tok)
	}
	return b.String()
}

// condition describes, for a failure of a then, the if it depends on: the
// setting it looks at and the values it is looking for, as in
// "remote.protocol is smb". loc is the section the if is about.
func (r *reporter) condition(failed string, loc []string) i18n.Message {
	ptr, ok := strings.CutPrefix(failed, schemaURL+"#")
	if !ok {
		return i18n.Message{}
	}
	i := strings.LastIndex(ptr, "/then")
	if i < 0 {
		return i18n.Message{}
	}
	cond, _ := r.lookup(ptr[:i] + "/if").(map[string]any)
	props, _ := cond["properties"].(map[string]any)
	for _, key := range sortedKeys(props) {
		test, _ := props[key].(map[string]any)
		var values []string
		if v, ok := test["const"]; ok {
			values = append(values, fmt.Sprint(v))
		}
		if vs, ok := test["enum"].([]any); ok {
			for _, v := range vs {
				values = append(values, fmt.Sprint(v))
			}
		}
		if len(values) > 0 {
			_, setting := r.name(append(slices.Clip(loc), key))
			return i18n.Msg("%s is %s", setting, strings.Join(values, " | "))
		}
	}
	return i18n.Message{}
}

// parent is the location of the section loc is in.
func parent(loc []string) []string {
	if len(loc) == 0 {
		return nil
	}
	return loc[:len(loc)-1]
}

// properties lists the settings the section at a schema location has.
func (r *reporter) properties(at string) []string {
	ptr, ok := strings.CutPrefix(at, schemaURL+"#")
	if !ok {
		return nil
	}
	section, _ := r.lookup(ptr).(map[string]any)
	props, _ := section["properties"].(map[string]any)
	return sortedKeys(props)
}

// lookup resolves a JSON pointer within the schema document.
func (r *reporter) lookup(ptr string) any {
	v := r.schema.doc
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		if tok == "" {
			continue
		}
		if t, err := url.PathUnescape(tok); err == nil {
			tok = t
		}
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		switch x := v.(type) {
		case map[string]any:
			v = x[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// typeNames says what a setting must be, from the JSON types it may have.
// Empty is never what anyone means to write, so it is left out.
func typeNames(types []string) any {
	var names []any
	for _, t := range types {
		var name any = t
		switch t {
		case "null":
			continue
		case "object":
			name = i18n.Msg("a section")
		case "array":
			name = i18n.Msg("a list")
		case "string":
			name = i18n.Msg("a string")
		case "integer":
			name = i18n.Msg("an integer")
		case "number":
			name = i18n.Msg("a number")
		case "boolean":
			name = i18n.Msg("true or false")
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	joined := names[0]
	for _, n := range names[1:] {
		joined = i18n.Msg("%s or %s", joined, n)
	}
	return joined
}

// gotName says what a setting was given instead.
func gotName(t string) any {
	switch t {
	case "null":
		return i18n.Msg("empty")
	case "boolean":
		return i18n.Msg("a boolean")
	}
	return typeNames([]string{t})
}

// show writes a value as it would be written in the file, or says it was
// left empty.
func show(v any) any {
	switch x := v.(type) {
	case nil:
		return i18n.Msg("empty")
	case string:
		return strconv.Quote(x)
	}
	return fmt.Sprint(v)
}

// suggest returns the setting a mistyped name most likely meant: the same name
// in other case; else the one setting it is the start of, or that is the
// start of it, as lock for lock_dir; else the nearest one at most two letters
// away from it.
func suggest(name string, choices []string) string {
	lower := strings.ToLower(name)
	var prefixed []string
	for _, c := range choices {
		if c == lower {
			return c
		}
		if len(lower) >= 3 && (strings.HasPrefix(c, lower) || strings.HasPrefix(lower, c)) {
			prefixed = append(prefixed, c)
		}
	}
	if len(prefixed) == 1 {
		return prefixed[0]
	}
	best, bestDist := "", 3
	for _, c := range choices {
		if d := editDistance(lower, c); d < bestDist && d < len(c) {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
