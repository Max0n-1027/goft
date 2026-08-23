package fsys

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"goft/internal/config"
)

// netrcEntry is one machine block from a .netrc file.
type netrcEntry struct {
	machine   string
	login     string
	password  string
	isDefault bool
}

// parseNetrc reads the token based format described in ftp(1).
//
// The one construct that cannot be handled by tokens alone is macdef: its body
// runs to the next blank line and must be skipped wholesale, otherwise the
// macro text is parsed as if it were directives.
func parseNetrc(r io.Reader) ([]netrcEntry, error) {
	var (
		entries  []netrcEntry
		cur      *netrcEntry
		pending  string
		inMacdef bool
	)

	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()

		if inMacdef {
			if strings.TrimSpace(line) == "" {
				inMacdef = false
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		for _, f := range fields {
			if pending != "" {
				switch pending {
				case "machine":
					flush()
					cur = &netrcEntry{machine: f}
				case "login":
					if cur != nil {
						cur.login = f
					}
				case "password":
					if cur != nil {
						cur.password = f
					}
				case "account", "macdef":
					// account is irrelevant here but must still consume its
					// argument; macdef's name is followed by its body.
				}
				if pending == "macdef" {
					inMacdef = true
					pending = ""
					break
				}
				pending = ""
				continue
			}

			switch f {
			case "machine", "login", "password", "account", "macdef":
				pending = f
			case "default":
				flush()
				cur = &netrcEntry{isDefault: true}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return entries, nil
}

// lookupNetrc returns the entry for host, falling back to the default entry.
func lookupNetrc(entries []netrcEntry, host string) (netrcEntry, string, bool) {
	for _, e := range entries {
		if !e.isDefault && strings.EqualFold(e.machine, host) {
			return e, "machine", true
		}
	}
	for _, e := range entries {
		if e.isDefault {
			return e, "default", true
		}
	}
	return netrcEntry{}, "", false
}

// netrcPath returns the file to read, following the same order of preference as
// the ftp and curl tools, with an explicit job setting taking priority.
func netrcPath(r config.Remote) string {
	if r.NetrcFile != "" {
		return r.NetrcFile
	}
	if p := os.Getenv("NETRC"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{filepath.Join(home, ".netrc")}
	if runtime.GOOS == "windows" {
		candidates = []string{filepath.Join(home, "_netrc"), filepath.Join(home, ".netrc")}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// resolveFTP merges the job configuration with ~/.netrc.
//
// Precedence is YAML, then the machine entry, then the default entry. Nothing
// falls back to anonymous access: a job that reaches the server without
// credentials is a configuration mistake, not an invitation to log in as
// somebody else.
func resolveFTP(r config.Remote) (*Resolved, error) {
	res := &Resolved{Host: r.Host, Port: r.Port, User: r.User, Password: r.Password}
	res.record("host", r.Host, SourceYAML)

	if r.Port != 0 {
		res.record("port", strconv.Itoa(r.Port), SourceYAML)
	} else {
		res.Port = 21
		res.record("port", "21", SourceDefault)
	}
	if r.User != "" {
		res.record("user", r.User, SourceYAML)
	}
	if r.Password.IsSet() {
		res.record("password", r.Password.String(), SourceYAML)
	}

	if (res.User != "" && res.Password.IsSet()) || !r.NetrcEnabled() {
		return res, nil
	}

	path := netrcPath(r)
	if path == "" {
		return res, nil
	}

	if warn := checkSecretPerm(path); warn != "" {
		// curl warns and carries on; refusing outright would break working
		// setups for a condition the operator may not control.
		res.Warnings = append(res.Warnings, warn)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open netrc %s: %w", path, err)
	}
	defer f.Close()

	entries, err := parseNetrc(f)
	if err != nil {
		return nil, fmt.Errorf("parse netrc %s: %w", path, err)
	}
	entry, kind, ok := lookupNetrc(entries, r.Host)
	if !ok {
		return res, nil
	}
	if res.User == "" && entry.login != "" {
		res.User = entry.login
		res.record("user", entry.login, SourceNetrc+" ("+kind+")")
	}
	if !res.Password.IsSet() && entry.password != "" {
		res.Password = config.Secret(entry.password)
		res.record("password", res.Password.String(), SourceNetrc+" ("+kind+")")
	}
	return res, nil
}

// checkSecretPerm reports a warning when a credentials file is readable by
// anyone other than its owner. Windows permissions do not map onto this model,
// so the check is skipped there rather than reporting something misleading.
func checkSecretPerm(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Sprintf("%s is readable by other users (mode %04o); chmod 600 it", path, mode)
	}
	return ""
}
