package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/viper"

	"goft/internal/i18n"
)

// Top-level keys of the main file, and the one key a job file may set.
const (
	keyService  = "service"
	keyJobFiles = "job_files"
	keyJobs     = "jobs"
)

// singleJobKeys are settings a job file from before goft ran several jobs at
// once had at its top level. Finding one there means a file in the old shape,
// which deserves a better answer than "not a setting".
var singleJobKeys = []string{"name", "local", "remote", "include", "exclude", "verify", "post_action"}

// Load reads the configuration at path and every job file it names, checks
// each file against the schema, replaces ${NAME} in their values from the
// environment, and decodes the result. A variable that is not set is an
// error. Validation of what the schema cannot see is performed separately by
// [Validate].
//
// Relative paths on this machine are resolved against the directory of the
// file they are written in, so a configuration means the same thing whatever
// directory goft is started from — which, for a service, nobody chooses.
func Load(path string) (*Config, error) {
	main, err := filepath.Abs(path)
	if err != nil {
		return nil, i18n.Errorf("read config: %w", err)
	}
	tree, err := readFile(main, false)
	if err != nil {
		return nil, err
	}

	c := &Config{SourceFile: main}
	base := filepath.Dir(main)

	svc, err := decodeService(main, tree[keyService])
	if err != nil {
		return nil, err
	}
	svc.Log.Path = under(base, svc.Log.Path)
	svc.LockDir = under(base, svc.LockDir)
	c.Service = svc

	jobs, err := decodeJobs(main, tree[keyJobs])
	if err != nil {
		return nil, err
	}
	c.Jobs = append(c.Jobs, jobs...)

	files, err := jobFiles(main, tree[keyJobFiles])
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		sub, err := readFile(file, true)
		if err != nil {
			return nil, err
		}
		jobs, err := decodeJobs(file, sub[keyJobs])
		if err != nil {
			return nil, err
		}
		c.Jobs = append(c.Jobs, jobs...)
	}
	c.JobFiles = files
	return c, nil
}

// readFile parses one file of the configuration, checks it against the
// schema, and expands the environment in its values. Every way the file
// breaks the schema is reported at once, each with its line.
//
// The schema sees the file as it is written, before the environment is
// expanded, which is how an editor sees it too: a setting such as a port may
// be written as ${NAME}, and its value is checked by [Validate] once it is
// known.
func readFile(path string, jobFile bool) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, i18n.Errorf("read config: %w", err)
	}
	doc, err := parseDocument(raw)
	if err != nil {
		return nil, i18n.Errorf("parse config %s: %w", path, err)
	}
	// A file in a shape goft no longer reads, or settings in a job file that
	// belong in the main one, deserve a better answer than the schema's "not
	// a setting".
	if tree, ok := doc.value.(map[string]any); ok {
		if err := checkOldFormat(path, tree); err != nil {
			return nil, err
		}
		if jobFile {
			if err := checkJobFile(path, tree); err != nil {
				return nil, err
			}
		}
	}
	if err := checkSchema(doc); err != nil {
		return nil, i18n.Errorf("invalid configuration %s:\n%w", path, err)
	}
	// The schema has made sure the file is a section.
	tree := doc.value.(map[string]any)
	if err := expandEnv(tree); err != nil {
		return nil, i18n.Errorf("config %s: %w", path, err)
	}
	return tree, nil
}

// checkOldFormat recognises a file written before goft ran several jobs at
// once, so that the error says how to convert it rather than only that its
// settings are not recognised.
func checkOldFormat(path string, tree map[string]any) error {
	for _, key := range singleJobKeys {
		if _, ok := tree[key]; ok {
			return i18n.Errorf("%s is a job file from before goft ran several jobs at once: move its settings under jobs:, as one item with a name and a direction", path)
		}
	}
	return nil
}

// checkJobFile refuses a job file that sets anything but jobs.
func checkJobFile(path string, tree map[string]any) error {
	var extra []string
	for key := range tree {
		if key != keyJobs {
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return i18n.Errorf("%s: a job file holds jobs and nothing else, but it sets %s",
			path, strings.Join(extra, ", "))
	}
	return nil
}

// decodeService reads the service section, defaults included. The schema has
// made sure it is a section, or left out.
func decodeService(path string, raw any) (Service, error) {
	v := viper.New()
	serviceDefaults(v)
	if m, ok := raw.(map[string]any); ok {
		if err := v.MergeConfigMap(m); err != nil {
			return Service{}, i18n.Errorf("decode config %s: %w", path, err)
		}
	}
	var s Service
	if err := v.Unmarshal(&s); err != nil {
		return Service{}, i18n.Errorf("decode config %s: %w", path, err)
	}
	return s, nil
}

// decodeJobs reads a jobs list, filling in each job's defaults and resolving
// its paths against the directory of the file it came from. The schema has
// made sure it is a list of sections, or left out.
func decodeJobs(path string, raw any) ([]*Job, error) {
	items, _ := raw.([]any)
	jobs := make([]*Job, 0, len(items))
	for _, item := range items {
		v := viper.New()
		jobDefaults(v)
		if err := v.MergeConfigMap(item.(map[string]any)); err != nil {
			return nil, i18n.Errorf("decode config %s: %w", path, err)
		}
		var j Job
		if err := v.Unmarshal(&j); err != nil {
			return nil, i18n.Errorf("decode config %s: %w", path, err)
		}
		j.SourceFile = path
		resolvePaths(&j, filepath.Dir(path))
		jobs = append(jobs, &j)
	}
	return jobs, nil
}

// jobFiles expands job_files into the files to read, in order, each once.
//
// A pattern with wildcards may match nothing — a jobs.d directory with no job
// in it yet is an ordinary state to be in — but a path written out in full
// must exist, since naming a file that is not there is a mistake.
func jobFiles(main string, raw any) ([]string, error) {
	// The schema has made sure job_files is one name, a list of them, or
	// left out.
	var patterns []string
	switch v := raw.(type) {
	case string:
		patterns = []string{v}
	case []any:
		for _, item := range v {
			patterns = append(patterns, item.(string))
		}
	}

	base := filepath.Dir(main)
	seen := map[string]bool{main: true}
	var files []string
	for _, pattern := range patterns {
		pattern = under(base, pattern)
		var matches []string
		if strings.ContainsAny(pattern, "*?[") {
			m, err := filepath.Glob(pattern)
			if err != nil {
				return nil, i18n.Errorf("job_files %q: %w", pattern, err)
			}
			sort.Strings(m)
			matches = m
		} else {
			if _, err := os.Stat(pattern); err != nil {
				return nil, i18n.Errorf("job_files: %w", err)
			}
			matches = []string{pattern}
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	return files, nil
}

// resolvePaths makes a job's paths on this machine absolute, against the
// directory of the file the job was written in.
func resolvePaths(j *Job, dir string) {
	j.Local.Path = under(dir, j.Local.Path)
	j.MoveTo = under(dir, j.MoveTo)
	j.Log.Path = under(dir, j.Log.Path)
	if j.Remote.IsLocal() {
		j.Remote.Path = under(dir, j.Remote.Path)
	}
	j.Remote.PrivateKey = under(dir, j.Remote.PrivateKey)
	j.Remote.KnownHosts = under(dir, j.Remote.KnownHosts)
	j.Remote.SSHConfigFile = under(dir, j.Remote.SSHConfigFile)
	j.Remote.NetrcFile = under(dir, j.Remote.NetrcFile)
	j.Remote.SSHAgent = under(dir, j.Remote.SSHAgent)
}

// PathKey is the form of a path on this machine that every spelling of one
// file shares: absolute and cleaned, and in lower case where file names are
// compared without regard to case, as on Windows and macOS. It tells whether
// two paths name the same file; it is never a path to open.
func PathKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}

// under resolves a relative path on this machine against dir.
//
// Only a path relative to the working directory is resolved. One starting
// with ~ or with an ssh_config token such as %d is relative to something else,
// and the code that reads it expands it. On Windows, one starting with a
// separator, \upload or /run/agent.sock, is relative to the root of the
// current drive rather than to any directory, and one naming a drive, C:data,
// to that drive's own working directory; neither means anything under dir, so
// both are left as written, as is a pipe name such as \\.\pipe\agent written
// in a configuration shared with Windows.
func under(dir, p string) string {
	switch {
	case p == "", filepath.IsAbs(p), filepath.VolumeName(p) != "":
		return p
	case strings.HasPrefix(p, "~"), strings.HasPrefix(p, "%"):
		return p
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, `\`):
		return p
	}
	return filepath.Join(dir, p)
}
