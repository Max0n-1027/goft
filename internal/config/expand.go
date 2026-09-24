package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/viper"
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
func expandEnv(v *viper.Viper) error {
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

	for _, key := range v.AllKeys() {
		switch val := v.Get(key).(type) {
		case string:
			if expanded := expand(key, val); expanded != val {
				v.Set(key, expanded)
			}
		case []any:
			changed := false
			out := make([]any, len(val))
			for i, item := range val {
				out[i] = item
				if s, ok := item.(string); ok {
					if expanded := expand(key, s); expanded != s {
						out[i], changed = expanded, true
					}
				}
			}
			if changed {
				v.Set(key, out)
			}
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("environment variables are not set: %s", strings.Join(missing, ", "))
	}
	return nil
}
