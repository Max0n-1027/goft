package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/spf13/viper"

	"goft/internal/i18n"
)

// schemaDoc returns the schema document, compiled as goft compiles it.
func schemaDoc(t *testing.T) map[string]any {
	t.Helper()
	s, err := compiled()
	if err != nil {
		t.Fatalf("the schema does not compile: %v", err)
	}
	return s.doc.(map[string]any)
}

// node walks the schema document by its keys.
func node(t *testing.T, doc any, path ...string) map[string]any {
	t.Helper()
	for _, key := range path {
		m, ok := doc.(map[string]any)
		if !ok {
			t.Fatalf("schema: %s is not an object", strings.Join(path, "/"))
		}
		doc = m[key]
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("schema: no object at %s", strings.Join(path, "/"))
	}
	return m
}

// tags lists the settings a struct is decoded from.
func tags(v any) []string {
	var out []string
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		if tag := typ.Field(i).Tag.Get("mapstructure"); tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func propertyNames(m map[string]any) []string {
	props, _ := m["properties"].(map[string]any)
	return sortedKeys(props)
}

// TestTheSchemaNamesEverySettingAndNoOther keeps the schema and the code that
// decodes the configuration in step: a setting added to one and not the other
// would be refused at startup, or accepted and ignored.
func TestTheSchemaNamesEverySettingAndNoOther(t *testing.T) {
	doc := schemaDoc(t)
	for _, tc := range []struct {
		where   string
		section map[string]any
		want    []string
	}{
		{"the main file", doc, []string{keyJobFiles, keyJobs, keyService}},
		{"service", node(t, doc, "properties", "service"), tags(Service{})},
		{"a job", node(t, doc, "definitions", "job"), tags(Job{})},
		{"local", node(t, doc, "definitions", "job", "properties", "local"), tags(Local{})},
		{"remote", node(t, doc, "definitions", "remote"), tags(Remote{})},
		{"retry", node(t, doc, "definitions", "job", "properties", "retry"), tags(Retry{})},
		{"log", node(t, doc, "definitions", "log"), tags(Log{})},
	} {
		if got := propertyNames(tc.section); !slices.Equal(got, tc.want) {
			t.Errorf("%s: the schema has %v, the code reads %v", tc.where, got, tc.want)
		}
		if tc.section["additionalProperties"] != false {
			t.Errorf("%s: the schema accepts settings it does not name", tc.where)
		}
	}
}

// enum returns the values a setting's first shape allows.
func enum(t *testing.T, setting map[string]any) []string {
	t.Helper()
	shapes, _ := setting["anyOf"].([]any)
	if len(shapes) == 0 {
		t.Fatalf("setting %v has no shapes", setting)
	}
	values, _ := shapes[0].(map[string]any)["enum"].([]any)
	var out []string
	for _, v := range values {
		out = append(out, v.(string))
	}
	return out
}

func strs[T ~string](vals ...T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

func TestTheSchemaAllowsTheValuesTheCodeKnows(t *testing.T) {
	doc := schemaDoc(t)
	job := node(t, doc, "definitions", "job", "properties")
	remote := node(t, doc, "definitions", "remote", "properties")
	log := node(t, doc, "definitions", "log", "properties")

	for _, tc := range []struct {
		setting string
		got     []string
		want    []string
	}{
		{"direction", enum(t, node(t, job, "direction")), strs(DirSend, DirRecv)},
		{"verify", enum(t, node(t, job, "verify")), strs(VerifyHash, VerifyLength, VerifyNone)},
		{"on_exists", enum(t, node(t, job, "on_exists")), strs(OnExistsSkip, OnExistsOverwrite)},
		{"post_action", enum(t, node(t, job, "post_action")), strs(PostNone, PostDelete, PostMove)},
		{"remote.protocol", enum(t, node(t, remote, "protocol")), strs(ProtocolFTP, ProtocolSFTP, ProtocolSMB, ProtocolLocal)},
		{"log.rotation", enum(t, node(t, log, "rotation")), strs(RotationSize, RotationDaily, RotationMonthly)},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s: the schema allows %v, the code %v", tc.setting, tc.got, tc.want)
		}
	}

	shapes := node(t, log, "fields")["anyOf"].([]any)
	items := shapes[0].(map[string]any)["items"].(map[string]any)
	if got := enum(t, items); !slices.Equal(got, SelectableLogFields) {
		t.Errorf("log.fields: the schema allows %v, the code %v", got, SelectableLogFields)
	}

	levels := enum(t, node(t, log, "level"))
	for _, level := range levels {
		if _, err := ParseLevel(level); err != nil {
			t.Errorf("log.level: the schema allows %q, which the code refuses", level)
		}
	}
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if !slices.Contains(levels, level) {
			t.Errorf("log.level: the schema does not allow %q", level)
		}
	}
}

// sameValue compares a default as viper holds it with one in the schema,
// reading both as durations where they are durations.
func sameValue(code, schema any) bool {
	if n, ok := schema.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return false
		}
		schema = f
	}
	if s, ok := code.(string); ok {
		if want, ok := schema.(string); ok {
			cd, err1 := time.ParseDuration(s)
			sd, err2 := time.ParseDuration(want)
			if err1 == nil && err2 == nil {
				return cd == sd
			}
			return s == want
		}
		return false
	}
	switch c := code.(type) {
	case int:
		f, ok := schema.(float64)
		return ok && float64(c) == f
	case float64:
		f, ok := schema.(float64)
		return ok && c == f
	case []string:
		list, ok := schema.([]any)
		if !ok || len(list) != len(c) {
			return false
		}
		for i := range c {
			if list[i] != c[i] {
				return false
			}
		}
		return true
	}
	return code == schema
}

// TestTheSchemaDefaultsAreTheCodesDefaults keeps the defaults an editor shows
// for a setting left out the same as the ones goft applies.
func TestTheSchemaDefaultsAreTheCodesDefaults(t *testing.T) {
	doc := schemaDoc(t)
	sections := map[string]map[string]any{
		"":       node(t, doc, "definitions", "job"),
		"remote": node(t, doc, "definitions", "remote"),
		"retry":  node(t, doc, "definitions", "job", "properties", "retry"),
		"log":    node(t, doc, "definitions", "log"),
	}
	v := viper.New()
	jobDefaults(v)
	for _, key := range v.AllKeys() {
		section, name := "", key
		if i := strings.LastIndexByte(key, '.'); i >= 0 {
			section, name = key[:i], key[i+1:]
		}
		props, _ := sections[section]["properties"].(map[string]any)
		setting, _ := props[name].(map[string]any)
		if setting == nil {
			t.Errorf("%s has a default in the code and is not in the schema", key)
			continue
		}
		if got := setting["default"]; !sameValue(v.Get(key), got) {
			t.Errorf("%s: the schema's default is %v, the code's %v", key, got, v.Get(key))
		}
	}

	// These default to on by being left nil, which the code reads as enabled.
	remote := node(t, doc, "definitions", "remote", "properties")
	var r Remote
	for name, enabled := range map[string]bool{
		"use_ssh_agent":          r.SSHAgentEnabled(),
		"use_ssh_config":         r.SSHConfigEnabled(),
		"use_netrc":              r.NetrcEnabled(),
		"use_credential_manager": r.CredentialManagerEnabled(),
	} {
		if got := node(t, remote, name)["default"]; got != enabled {
			t.Errorf("remote.%s: the schema's default is %v, the code's %v", name, got, enabled)
		}
	}
}

// TestTheExampleOnlyMentionsRealSettings reads goft.example.yaml's comments,
// where most settings are shown switched off, for anything written as a
// setting the schema does not have.
func TestTheExampleOnlyMentionsRealSettings(t *testing.T) {
	doc := schemaDoc(t)
	known := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if props, ok := x["properties"].(map[string]any); ok {
				for k := range props {
					known[k] = true
				}
			}
			for _, item := range x {
				collect(item)
			}
		case []any:
			for _, item := range x {
				collect(item)
			}
		}
	}
	collect(doc)

	raw, err := os.ReadFile(filepath.Join("..", "..", "goft.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// A setting switched off is a key, then nothing, one word, or a list or a
	// section, and maybe a comment of its own: never a sentence.
	setting := regexp.MustCompile(`^\s*#\s+([a-z_]+):(\s*|\s+(\S+|\[.*\]|\{.*\}))(\s{2,}#.*)?$`)
	found := 0
	for i, line := range strings.Split(string(raw), "\n") {
		m := setting.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		if !known[m[1]] {
			t.Errorf("goft.example.yaml:%d: %s is not a setting", i+1, m[1])
		}
	}
	if found < 20 {
		t.Errorf("found only %d settings in the example's comments; the search is not finding them", found)
	}
}

// TestTheDocumentedConfigurationsFollowTheSchema checks every whole file the
// README and the pages under docs show.
func TestTheDocumentedConfigurationsFollowTheSchema(t *testing.T) {
	root := filepath.Join("..", "..")
	pages, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	pages = append(pages, filepath.Join(root, "README.md"), filepath.Join(root, "README-ja.md"))

	block := regexp.MustCompile("(?s)```yaml\n(.*?)```")
	checked := 0
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range block.FindAllStringSubmatch(string(raw), -1) {
			if !wholeFile(m[1]) {
				continue
			}
			checked++
			doc, err := parseDocument([]byte(m[1]))
			if err != nil {
				t.Errorf("%s: %v\n%s", page, err, m[1])
				continue
			}
			if err := checkSchema(doc); err != nil {
				t.Errorf("%s: %v\n%s", page, err, m[1])
			}
		}
	}
	if checked < 6 {
		t.Errorf("checked only %d files; the search is not finding them", checked)
	}
}

// wholeFile reports whether a YAML block is a file of goft's rather than a
// piece of one: its first setting is one the main file starts with. A block
// that shows mistakes on purpose says so in its first line, and is left out.
func wholeFile(yaml string) bool {
	first, _, _ := strings.Cut(yaml, "\n")
	if strings.Contains(first, "mistake") || strings.Contains(first, "誤り") {
		return false
	}
	for _, line := range strings.Split(yaml, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _ := strings.Cut(line, ":")
		return key == keyService || key == keyJobs || key == keyJobFiles
	}
	return false
}

// schemaProblems checks YAML against the schema, without the environment
// being expanded, and returns what goft would report.
func schemaProblems(t *testing.T, yaml string) string {
	t.Helper()
	doc, err := parseDocument([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := checkSchema(doc); err != nil {
		return err.Error()
	}
	return ""
}

// job is a valid job, as an item of the jobs list, with extra settings at
// the job's level.
func job(extra string) string {
	return "jobs:\n" +
		"  - name: a\n" +
		"    direction: send\n" +
		"    local:\n" +
		"      path: /a\n" +
		"    remote:\n" +
		"      protocol: sftp\n" +
		"      host: h\n" +
		"      path: /p\n" + extra
}

func TestSchemaProblemsAreExplainedInGoftsTerms(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"misspelt", job("    post_acton: delete\n"), "line 10: job a: post_acton is not a setting (did you mean post_action?)"},
		{"capitalised", job("    Recursive: true\n"), "line 10: job a: Recursive is not a setting (did you mean recursive?)"},
		{"unknown", job("    colour: red\n"), "line 10: job a: colour is not a setting"},
		{"unknown among few", "service:\n  colour: red\n", "line 2: service.colour is not a setting (lock_dir | log)"},
		{"the start of a name", "service:\n  lock: /x\n", "line 2: service.lock is not a setting (did you mean lock_dir?)"},
		{"top level", "job:\n  - x\n", "line 1: job is not a setting (did you mean jobs?)"},
		{"numeric key", job("    1: x\n"), "line 10: job a: 1 is not a setting"},
		{"required", "jobs:\n  - name: a\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p}\n", "line 2: job a: direction is required"},
		{"required section", "jobs:\n  - name: a\n    direction: send\n    remote: {protocol: sftp, host: h, path: /p}\n", "line 2: job a: local is required"},
		{"required in a section", "jobs:\n  - name: a\n    direction: send\n    local: {}\n    remote: {protocol: sftp, host: h, path: /p}\n", "line 4: job a: local.path is required"},
		{"host", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote:\n      protocol: ftp\n      path: /p\n", "line 5: job a: remote.host is required when remote.protocol is ftp | sftp | smb"},
		{"share", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote:\n      protocol: smb\n      host: h\n      path: /p\n", "line 5: job a: remote.share is required when remote.protocol is smb"},
		{"move_to", job("    post_action: move\n"), "line 2: job a: move_to is required when post_action is move"},
		{"not used", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote:\n      protocol: local\n      path: /b\n      user: me\n", "line 8: job a: remote.user is not used when remote.protocol is local"},
		{"integer", job("    workers: \"4\"\n"), "line 10: job a: workers must be an integer, not a string"},
		{"string", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, password: 1234}\n", "line 5: job a: remote.password must be a string, not a number: put the value in quotes"},
		{"string from a boolean", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, user: true}\n", "line 5: job a: remote.user must be a string, not a boolean: put the value in quotes"},
		{"boolean", job("    enabled: \"yes\"\n"), "line 10: job a: enabled must be true or false, not a string"},
		{"list", job("    include: \"*.csv\"\n"), "line 10: job a: include must be a list, not a string"},
		{"empty section", "jobs:\n  - name: a\n    direction: send\n    local:\n    remote: {protocol: sftp, host: h, path: /p}\n", "line 4: job a: local must be a section, not empty"},
		{"not a file of settings", "- a\n- b\n", "line 1: the file must be a section, not a list"},
		{"choice", job("    verify: sha\n"), `line 10: job a: verify "sha" is invalid (hash | length | none)`},
		{"choice in capitals", job("    log:\n      level: INFO\n"), `line 11: job a: log.level "INFO" is invalid (debug | info | warn | warning | error)`},
		{"choice in a list", job("    log:\n      fields: [src, size]\n"), `line 11: job a: log.fields[1] "size" is invalid (src | dst | protocol | bytes | duration_ms | verify | result | reason | error | hash_src | hash_dst | attempt | rate_mibs)`},
		{"choice left empty", job("    verify:\n"), "line 10: job a: verify is empty (hash | length | none)"},
		{"duration left empty", job("    poll_interval:\n"), "line 10: job a: poll_interval must be a duration such as 30s, 5m or 1h30m, not empty"},
		{"minimum", job("    workers: 0\n"), "line 10: job a: workers must be >= 1"},
		{"fraction", job("    retry:\n      backoff: 0.5\n"), "line 11: job a: retry.backoff must be >= 1"},
		{"maximum", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, port: 70000}\n", "line 5: job a: remote.port must be <= 65535"},
		{"no name", "jobs:\n  - name: \"\"\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p}\n", "line 2: job #1: name must not be empty"},
		{"a job that is not a section", "jobs:\n  - x\n", "line 2: job #1 must be a section, not a string"},
		{"duration without a unit", job("    poll_interval: 5\n"), "line 10: job a: poll_interval must be a duration such as 30s, 5m or 1h30m, not 5"},
		{"duration of the wrong type", job("    poll_interval: [5s]\n"), "line 10: job a: poll_interval must be a duration such as 30s, 5m or 1h30m, not [5s]"},
		{"duration in words", job("    stable_duration: 3 seconds\n"), `line 10: job a: stable_duration must be a duration such as 30s, 5m or 1h30m, not "3 seconds"`},
		{"one of several types", "job_files: {a: b}\n", "line 1: job_files must be a string or a list, not a section"},
		{"in a list of several types", "job_files: [ok.yaml, 1]\n", "line 1: job_files[1] must be a string, not a number: put the value in quotes"},
		{"not a number", job("    max_file_size_mb: .inf\n"), "line 10: job a: max_file_size_mb must be an integer, not a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := schemaProblems(t, tc.yaml)
			if !slices.Contains(strings.Split(got, "\n"), tc.want) {
				t.Errorf("got:\n%s\nwant the line:\n%s", got, tc.want)
			}
		})
	}
}

func TestSchemaAcceptsWhatGoftAccepts(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"an empty file", ""},
		{"only comments", "# nothing yet\n"},
		{"null", "~\n"},
		{"empty sections", "service:\njob_files:\njobs:\n"},
		{"empty job sections", job("    retry:\n    log:\n")},
		{"one job file", "job_files: jobs.d/*.yaml\n"},
		{"references", job(
			"    enabled: ${ON}\n" +
				"    workers: ${WORKERS}\n" +
				"    verify: ${VERIFY}\n" +
				"    poll_interval: ${SECONDS}s\n" +
				"    include: ${PATTERNS}\n" +
				"    log:\n" +
				"      fields: [src, \"${FIELD}\"]\n" +
				"      max_backups: ${KEEP}\n")},
		{"a reference for the port", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, port: \"${PORT}\"}\n"},
		{"a date as text", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, user: 2026-11-12}\n"},
		{"durations", job("    poll_interval: 1h30m\n    stable_duration: 0\n    retry:\n      interval: 1.5s\n      backoff: 1\n")},
		{"zero turning a timeout off", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: sftp, host: h, path: /p, io_timeout: 0}\n"},
		{"a local copy", "jobs:\n  - name: a\n    direction: send\n    local: {path: /a}\n    remote: {protocol: local, path: /b}\n"},
		{"a whole number as a fraction", job("    retry:\n      backoff: 2.5\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaProblems(t, tc.yaml); got != "" {
				t.Errorf("refused:\n%s", got)
			}
		})
	}
}

func TestSchemaProblemsComeInTheOrderOfTheFile(t *testing.T) {
	got := schemaProblems(t, job("    workers: 0\n    colour: red\n    verify: sha\n"))
	want := "line 10: job a: workers must be >= 1\n" +
		"line 11: job a: colour is not a setting\n" +
		`line 12: job a: verify "sha" is invalid (hash | length | none)`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSchemaProblemsAreFoundThroughAMerge(t *testing.T) {
	yaml := "jobs:\n" +
		"  - &base\n" +
		"    name: a\n" +
		"    direction: send\n" +
		"    local: {path: /a}\n" +
		"    remote: {protocol: sftp, host: h, path: /p}\n" +
		"    workerz: 2\n" +
		"  - <<: *base\n" +
		"    name: b\n" +
		"  - <<: [*base]\n" +
		"    name: c\n"
	got := schemaProblems(t, yaml)
	for _, want := range []string{
		"line 7: job a: workerz is not a setting (did you mean workers?)",
		"line 7: job b: workerz is not a setting (did you mean workers?)",
		"line 7: job c: workerz is not a setting (did you mean workers?)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("got:\n%s\nwant the line:\n%s", got, want)
		}
	}
}

func TestSchemaProblemsAreTranslated(t *testing.T) {
	doc, err := parseDocument([]byte(job("    post_acton: delete\n    workers: \"4\"\n")))
	if err != nil {
		t.Fatal(err)
	}
	got := i18n.Japanese.Error(checkSchema(doc))
	for _, want := range []string{
		"10 行目: ジョブ a: post_acton という設定はありません（post_action の書き間違いではありませんか？）",
		"11 行目: ジョブ a: workers は整数で書いてください（文字列になっています）",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("got:\n%s\nwant the line:\n%s", got, want)
		}
	}
}

func TestLoadReportsSchemaProblemsWithTheFileTheyAreIn(t *testing.T) {
	dir := t.TempDir()
	more := writeFile(t, dir, "jobs.d/more.yaml", "jobs:\n"+sendJob("b", "/b")+"    workers: many\n")
	main := writeFile(t, dir, "goft.yaml", "job_files: [jobs.d/more.yaml]\njobs:\n"+sendJob("a", "/a"))
	_, err := Load(main)
	want := "invalid configuration " + more + ":\nline 10: job b: workers must be an integer, not a string"
	if err == nil || err.Error() != want {
		t.Errorf("Load() = %v, want %q", err, want)
	}
}

func TestLoadChecksTheFileAsWrittenAndTheValueOnceKnown(t *testing.T) {
	t.Setenv("GOFT_TEST_WORKERS", "0")
	main := writeFile(t, t.TempDir(), "goft.yaml", "jobs:\n"+sendJob("a", t.TempDir())+"    workers: ${GOFT_TEST_WORKERS}\n")
	cfg, err := Load(main)
	if err != nil {
		t.Fatalf("Load() = %v, want ${NAME} accepted where a number goes", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "workers must be >= 1") {
		t.Errorf("Validate() = %v, want the value checked once it is known", err)
	}
}

func TestLoadReportsAKeyGivenTwice(t *testing.T) {
	main := writeFile(t, t.TempDir(), "goft.yaml", "jobs:\njobs:\n")
	if _, err := Load(main); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("Load() = %v, want the file refused as YAML", err)
	}
}

func TestSuggest(t *testing.T) {
	choices := []string{"include", "exclude", "name", "workers"}
	for name, want := range map[string]string{
		"Name":     "name",
		"WORKERS":  "workers",
		"workerz":  "workers",
		"incldue":  "include",
		"exlcude":  "exclude",
		"colour":   "",
		"n":        "",
		"includes": "include",
	} {
		if got := suggest(name, choices); got != want {
			t.Errorf("suggest(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"日本", "日本語", 1},
	} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDocumentLineAndValueLookups(t *testing.T) {
	doc, err := parseDocument([]byte("a:\n  b: [x, y]\n  c: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		loc  []string
		line int
	}{
		{nil, 1},
		{[]string{"a"}, 1},
		{[]string{"a", "c"}, 3},
		{[]string{"a", "b", "1"}, 2},
		{[]string{"a", "missing"}, 1},
		{[]string{"a", "b", "9"}, 2},
		{[]string{"a", "b", "x"}, 2},
		{[]string{"a", "c", "deeper"}, 3},
	} {
		if got := doc.line(tc.loc); got != tc.line {
			t.Errorf("line(%v) = %d, want %d", tc.loc, got, tc.line)
		}
	}
	for _, loc := range [][]string{{"z"}, {"a", "b", "2"}, {"a", "b", "x"}, {"a", "c", "d"}} {
		if v, ok := doc.at(loc); ok {
			t.Errorf("at(%v) = %v, want nothing", loc, v)
		}
	}
	if v, ok := doc.at([]string{"a", "b", "1"}); !ok || v != "y" {
		t.Errorf(`at(a, b, 1) = %v, %v, want "y"`, v, ok)
	}

	empty, err := parseDocument(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.line([]string{"a"}); got != 0 {
		t.Errorf("line in an empty file = %d, want 0", got)
	}
}

func TestABrokenSchemaDoesNotCompile(t *testing.T) {
	for name, src := range map[string]string{
		"not JSON":       "{",
		"not a schema":   `{"type": 5}`,
		"a dangling ref": `{"$ref": "#/definitions/none"}`,
	} {
		if _, err := compileSchema(src); err == nil {
			t.Errorf("%s: compiled, want an error", name)
		}
	}
}

// TestReporterExplainsFailuresTheSchemaDoesNotHaveYet feeds the reporter the
// failures goft's schema cannot produce today, so that a schema that grows a
// new rule still gets a sensible message, if not a tailored one.
func TestReporterExplainsFailuresTheSchemaDoesNotHaveYet(t *testing.T) {
	s, err := compiled()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocument([]byte(job("    x: 1\n")))
	if err != nil {
		t.Fatal(err)
	}
	r := reporter{doc: doc, schema: s}
	at := []string{"jobs", "0", "x"}
	r.explain(&jsonschema.ValidationError{SchemaURL: schemaURL + "#/definitions/job", InstanceLocation: at, ErrorKind: &kind.FalseSchema{}})
	r.explain(&jsonschema.ValidationError{SchemaURL: schemaURL + "#/definitions/job", InstanceLocation: at, ErrorKind: &kind.Const{Got: 1.0, Want: 2.0}})
	r.explain(&jsonschema.ValidationError{SchemaURL: "file:///elsewhere.json#/then", InstanceLocation: at[:2], ErrorKind: &kind.Required{Missing: []string{"y"}}})
	r.explain(&jsonschema.ValidationError{SchemaURL: "file:///elsewhere.json", InstanceLocation: at[:2], ErrorKind: &kind.AdditionalProperties{Properties: []string{"z"}}})
	var got []string
	for _, p := range r.problems {
		got = append(got, p.err.Error())
	}
	want := []string{
		"job a: x is not a setting",
		"job a: x: value must be 2",
		"job a: y is required",
		"job a: z is not a setting",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if v := r.lookup("/definitions/job/allOf/9"); v != nil {
		t.Errorf("lookup past the end of a list = %v, want nothing", v)
	}
	if v := r.lookup("/title/deeper"); v != nil {
		t.Errorf("lookup inside a string = %v, want nothing", v)
	}
	if v, _ := r.lookup("/definitions/job/properties/name").(map[string]any); v["type"] != "string" {
		t.Errorf("lookup of a setting = %v", v)
	}
	if parent(nil) != nil {
		t.Error("parent of the file is not nothing")
	}
	if got := typeNames([]string{"null"}); got != "" {
		t.Errorf("typeNames(null) = %v, want nothing", got)
	}
}

func TestLoadReportsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOFT_TEST_WORKERS", "many")
	for _, tc := range []struct{ name, body, want string }{
		{"missing", "", "read config"},
		{"not YAML", "jobs: [\n", "parse config"},
		{"a value that is not a number", "jobs:\n" + sendJob("a", "/a") + "    workers: ${GOFT_TEST_WORKERS}\n", "decode config"},
		{"a bad pattern", "job_files: \"jobs.d/[\"\n", "job_files"},
	} {
		path := filepath.Join(dir, tc.name+".yaml")
		if tc.body != "" {
			writeFile(t, dir, tc.name+".yaml", tc.body)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Load() = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestOneJobFileMayBeNamedWithoutAList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "jobs.d/a.yaml", "jobs:\n"+sendJob("a", "/a"))
	cfg, err := Load(writeFile(t, dir, "goft.yaml", "job_files: jobs.d/*.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Jobs) != 1 || cfg.Jobs[0].Name != "a" {
		t.Errorf("jobs = %v, want job a from the one pattern", cfg.Jobs)
	}
}

func TestProblemsOnOneLineComeInTheSameOrderEveryTime(t *testing.T) {
	yaml := "jobs:\n  - {name: a, direction: up, local: {path: /a}, remote: {protocol: ftp, host: h, path: /p, port: x, user: 1, share: 2}, workers: 0, verify: no}\n"
	first := schemaProblems(t, yaml)
	if strings.Count(first, "\n") < 4 {
		t.Fatalf("want several problems on line 2, got:\n%s", first)
	}
	for range 20 {
		if got := schemaProblems(t, yaml); got != first {
			t.Fatalf("the order changed:\n%s\nthen:\n%s", first, got)
		}
	}
}
