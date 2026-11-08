package config

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"goft/internal/i18n"
)

// validateAcrossJobs checks the jobs against each other.
//
// Each check is about something one process running all of them would get
// wrong, and none of them can be seen by looking at a job on its own:
//
//   - two jobs with the same name, which the command line, the log and the
//     job's lock could not tell apart;
//   - two jobs reading the same place where one of them deletes or moves what
//     it sends, so the other misses files or sends them half gone;
//   - jobs that pass files round in a loop, each one's destination another's
//     source until the files come back to where they started;
//   - one log file written with two different rotation settings, which would
//     have two writers rotating it under each other.
//
// Only enabled jobs are checked for the second and third: a job the service
// does not run cannot race the ones it does. A job passing files on to another
// — recv into a staging directory that a send job takes from — is a relay, not
// a loop, and is left alone.
func validateAcrossJobs(c *Config, add addFunc) {
	seen := map[string]*Job{}
	for _, j := range c.Jobs {
		if strings.TrimSpace(j.Name) == "" {
			continue
		}
		folded := strings.ToLower(j.Name)
		if first, ok := seen[folded]; ok {
			add(i18n.Errorf("job %q in %s has the same name as job %q in %s (names that differ only in case count as the same)",
				j.Name, j.SourceFile, first.Name, first.SourceFile))
			continue
		}
		seen[folded] = j
	}

	enabled := c.Enabled()
	for i, a := range enabled {
		for _, b := range enabled[i+1:] {
			if !source(a).overlaps(source(b)) {
				continue
			}
			if consumes(a) || consumes(b) {
				add(i18n.Errorf("jobs %q and %q both take files from %s, and one of them deletes or moves what it sends, so the other would miss files or send them half gone",
					a.Name, b.Name, source(a)))
			}
		}
	}

	for _, loop := range loops(enabled) {
		add(i18n.Errorf("jobs pass files round in a loop: %s", strings.Join(loop, " -> ")))
	}

	validateSharedLogs(c, add)
}

// consumes reports whether a job takes its files away once they are sent.
func consumes(j *Job) bool { return j.PostAction == PostDelete || j.PostAction == PostMove }

// location is a place files are read from or written to: a directory on this
// machine, or a directory on a server.
type location struct {
	// host is empty for a directory on this machine, and otherwise names the
	// server the way the configuration does: protocol, host, port and share.
	host string
	dir  string
}

func (l location) String() string {
	if l.host == "" {
		return l.dir
	}
	return l.host + l.dir
}

// overlaps reports whether one location is the other or lies within it.
func (l location) overlaps(o location) bool {
	if l.host != o.host {
		return false
	}
	if l.host == "" {
		in, _ := isInside(l.dir, o.dir)
		out, _ := isInside(o.dir, l.dir)
		return in || out
	}
	return remoteInside(l.dir, o.dir) || remoteInside(o.dir, l.dir)
}

func remoteInside(parent, child string) bool {
	return parent == "/" || child == parent || strings.HasPrefix(child, parent+"/")
}

// source and destination are where a job reads its files and where it writes
// them.
func source(j *Job) location {
	if j.Direction == DirRecv {
		return remoteLocation(j.Remote)
	}
	return location{dir: filepath.Clean(j.Local.Path)}
}

func destination(j *Job) location {
	if j.Direction == DirRecv {
		return location{dir: filepath.Clean(j.Local.Path)}
	}
	return remoteLocation(j.Remote)
}

// remoteLocation names the remote side of a job. A server is known only by the
// name the configuration gives it, so one reached by two different names, an
// ssh_config alias and its address, say, counts as two servers.
func remoteLocation(r Remote) location {
	if r.IsLocal() {
		return location{dir: filepath.Clean(r.Path)}
	}
	port := r.Port
	if port == 0 {
		port = defaultPort[r.Protocol]
	}
	host := string(r.Protocol) + "://" + strings.ToLower(r.Host) + ":" + strconv.Itoa(port)
	if r.Protocol == ProtocolSMB {
		host += "/" + strings.ToLower(r.Share)
	}
	return location{host: host, dir: path.Clean("/" + r.Path)}
}

// defaultPort is the port each protocol is reached on when none is given.
var defaultPort = map[Protocol]int{ProtocolFTP: 21, ProtocolSFTP: 22, ProtocolSMB: 445}

// loops finds the cycles among jobs, where one job's destination is another's
// source, each reported once, starting from its first job in the
// configuration.
func loops(jobs []*Job) [][]string {
	next := make([][]int, len(jobs))
	for i, a := range jobs {
		for k, b := range jobs {
			if i != k && destination(a).overlaps(source(b)) {
				next[i] = append(next[i], k)
			}
		}
	}

	var (
		found  [][]string
		inLoop = map[string]bool{}
	)
	for start := range jobs {
		// A walk from start that comes back to it is a loop. Walking only to
		// jobs after start finds each loop once, from its first member.
		var walk func(at int, trail []int) bool
		walk = func(at int, trail []int) bool {
			for _, k := range next[at] {
				switch {
				case k == start:
					names := make([]string, 0, len(trail)+1)
					for _, t := range trail {
						names = append(names, jobs[t].Name)
					}
					names = append(names, jobs[start].Name)
					key := strings.Join(names, "\x00")
					if !inLoop[key] {
						inLoop[key] = true
						found = append(found, names)
					}
					return true
				case k > start && !contains(trail, k):
					if walk(k, append(trail, k)) {
						return true
					}
				}
			}
			return false
		}
		walk(start, []int{start})
	}
	return found
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// validateSharedLogs checks that everything writing one log file agrees on how
// it is rotated. One process opens the file once and every writer shares it,
// so the settings that decide its rotation can only have one value.
func validateSharedLogs(c *Config, add addFunc) {
	type writer struct {
		who string
		log Log
	}
	// Two spellings of one file are one file, as the process opening it will
	// find; the first spelling is the one reported.
	byKey := map[string][]writer{}
	shown := map[string]string{}
	note := func(who string, l Log) {
		if l.Path == "" {
			return
		}
		key := PathKey(l.Path)
		if _, ok := shown[key]; !ok {
			shown[key] = filepath.Clean(l.Path)
		}
		byKey[key] = append(byKey[key], writer{who, l})
	}
	note("service", c.Service.Log)
	for _, j := range c.Jobs {
		note(fmt.Sprintf("%q", j.Name), j.Log)
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ws := byKey[k]
		first := ws[0]
		for _, w := range ws[1:] {
			if !sameRotation(first.log, w.log) {
				add(i18n.Errorf("log file %s is written by %s and %s with different rotation settings; one file is rotated one way", shown[k], first.who, w.who))
			}
		}
	}
}

func sameRotation(a, b Log) bool {
	return a.Rotation == b.Rotation && a.MaxSizeMB == b.MaxSizeMB &&
		a.MaxBackups == b.MaxBackups && a.MaxAgeDays == b.MaxAgeDays && a.Compress == b.Compress
}
