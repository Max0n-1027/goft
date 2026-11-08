package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"goft/internal/i18n"
)

// variable matches a reference to the environment: ${NAME}, and nothing else.
// A dollar sign not followed by a brace is just a character, so a password is
// free to contain one.
var variable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces every ${NAME} in the values of the configuration with the
// environment variable of that name.
//
// It works on the parsed values rather than on the text of the file, so a
// comment naming a variable nobody has set — goft.example.yaml is full of them —
// is not an error, and a key is never rewritten.
//
// A variable that is not set is an error. Expanding it to an empty string, as
// the shell would, turned a forgotten export into an empty password that
// surfaced much later as an authentication failure with no hint as to why. A
// variable set to the empty string is accepted: that is a decision someone made.
func expandEnv(tree map[string]any) error {
	var missing []string
	expand := func(key, s string) string {
		return variable.ReplaceAllStringFunc(s, func(ref string) string {
			name := variable.FindStringSubmatch(ref)[1]
			value, ok := os.LookupEnv(name)
			if !ok {
				missing = append(missing, fmt.Sprintf("%s (in %s)", name, key))
			}
			return value
		})
	}

	// Values are reached wherever they are: in a section, in a list, in a
	// section in a list — which is where every job's settings are.
	var walk func(key string, v any) any
	walk = func(key string, v any) any {
		switch x := v.(type) {
		case string:
			return expand(key, x)
		case map[string]any:
			for k, item := range x {
				child := k
				if key != "" {
					child = key + "." + k
				}
				x[k] = walk(child, item)
			}
			return x
		case []any:
			for i, item := range x {
				x[i] = walk(fmt.Sprintf("%s[%d]", key, i), item)
			}
			return x
		default:
			return v
		}
	}
	walk("", tree)

	if len(missing) > 0 {
		sort.Strings(missing)
		return i18n.Errorf("environment variables are not set: %s", strings.Join(missing, ", "))
	}
	return nil
}
