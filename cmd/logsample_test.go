package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"goft/internal/sftptest"
)

// TestGenerateLogSample writes the log files quoted in docs/log-example.md, so
// that the documented output is a real run rather than something typed out by
// hand. It is skipped unless GOFT_LOG_SAMPLE names the directory to write into:
//
//	GOFT_LOG_SAMPLE=/tmp/sample go test ./cmd -run TestGenerateLogSample
//
// The scenario covers a transfer, a file over the size cap, a destination the
// account cannot write to, a second pass over files already delivered, a
// comparison that finds them identical, an unreachable server, and a rename
// that keeps failing until the attempts run out.
func TestGenerateLogSample(t *testing.T) {
	out := os.Getenv("GOFT_LOG_SAMPLE")
	if out == "" {
		t.Skip("set GOFT_LOG_SAMPLE to the output directory")
	}

	localDir := t.TempDir()
	remoteDir := t.TempDir()
	remote := sftptest.Start(t, remoteDir)

	write := func(dir, name, body string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfgFor := func(local, logPath, level, extra string, port int) string {
		body := fmt.Sprintf(`
name: invoice-upload
local:
  path: %s
remote:
  protocol: sftp
  host: %s
  port: %d
  user: %s
  password: %s
  known_hosts: %s
  path: %s
  use_ssh_config: false
recursive: true
include: ["*.csv", "*.dat"]
max_file_size_mb: 1
stable_duration: 3s
workers: 2
verify: hash
%s
log:
  path: %s
  level: %s
`, local, remote.Host, port, remote.User, string(remote.Password),
			remote.KnownHosts, remoteDir, extra, logPath, level)

		p := filepath.Join(t.TempDir(), "job.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	run := func(cfgPath string, args ...string) {
		resetFlags()
		rootCmd.SetArgs(append(args, "-c", cfgPath, "--no-console"))
		rootCmd.SetOut(os.Stdout)
		Execute()
	}

	write(localDir, "invoice_202608_01.csv", "id,amount\n1,1200\n")
	write(localDir, "invoice_202608_02.csv", "id,amount\n2,980\n")
	write(localDir, "archive.dat", strings.Repeat("x", 2<<20)) // over max_file_size_mb
	write(localDir, "restricted/invoice_202608_03.csv", "id,amount\n3,1100\n")

	// A destination directory the account cannot write to, so that one file
	// fails while the others go through.
	if err := os.MkdirAll(filepath.Join(remoteDir, "restricted"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(remoteDir, "restricted"), 0o700) })

	info := filepath.Join(out, "goft-info.log")
	os.Remove(info)
	run(cfgFor(localDir, info, "info", "on_exists: skip", remote.Port), "send")
	// A second pass, with everything already at the far end.
	run(cfgFor(localDir, info, "info", "on_exists: skip", remote.Port), "send")
	// And one that compares before deciding.
	run(cfgFor(localDir, info, "info", "on_exists: overwrite", remote.Port), "send")

	// An unreachable server, to show what an outage and its retries look like.
	outage := t.TempDir()
	write(outage, "invoice_202608_04.csv", "id,amount\n4,1400\n")
	run(cfgFor(outage, info, "info",
		"on_exists: skip\nretry:\n  max_attempts: 3\n  interval: 1s\n  backoff: 2",
		deadPort(t)), "send")

	// A destination that cannot be replaced, to show a retry and its backoff.
	blocked := t.TempDir()
	write(blocked, "invoice_202608_05.csv", "id,amount\n5,1500\n")
	write(remoteDir, "invoice_202608_05.csv/held-open", "in the way")
	run(cfgFor(blocked, info, "info",
		"on_exists: overwrite\nretry:\n  max_attempts: 3\n  interval: 1s\n  backoff: 2",
		remote.Port), "send")

	debug := filepath.Join(out, "goft-debug.log")
	os.Remove(debug)
	run(cfgFor(localDir, debug, "debug", "on_exists: overwrite", remote.Port), "send")

	for _, p := range []string{info, debug} {
		sanitize(t, p, localDir, outage, blocked, remoteDir, remote.Host, remote.Port)
	}
}

// sanitize rewrites the throwaway details of the test rig into the job the
// documentation describes. Only paths, the host and the port are touched: the
// records themselves, their order and their measurements are as goft wrote
// them.
func sanitize(t *testing.T, path string, local, outage, blocked, remote, host string, port int) {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, dir := range []string{local, outage, blocked} {
		text = strings.ReplaceAll(text, dir, "/data/out/invoice")
	}
	text = strings.ReplaceAll(text, remote, "/upload/invoice")
	text = strings.ReplaceAll(text, host, "invoice-sftp")
	text = strings.ReplaceAll(text, "\"tester\"", "\"uploader\"")
	text = regexp.MustCompile(`"port":\d+`).ReplaceAllString(text, `"port":22`)
	text = regexp.MustCompile(`"field":"port","value":"\d+"`).
		ReplaceAllString(text, `"field":"port","value":"22"`)
	// The identity files come from the home directory of whoever ran the test.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		text = strings.ReplaceAll(text, home, "/home/svc-transfer")
	}
	text = regexp.MustCompile(`/tmp/[^"]*?/known_hosts`).
		ReplaceAllString(text, "/home/svc-transfer/.ssh/known_hosts")
	text = regexp.MustCompile(`/tmp/[^"]*?/job\.yaml`).
		ReplaceAllString(text, "/etc/goft/invoice-upload.yaml")
	text = regexp.MustCompile(`"path":"[^"]*?goft-(info|debug)\.log"`).
		ReplaceAllString(text, `"path":"/var/log/goft/invoice-upload.log"`)
	text = regexp.MustCompile(`dial invoice-sftp:\d+: dial tcp invoice-sftp:\d+:`).
		ReplaceAllString(text, "dial invoice-sftp:22: dial tcp 10.0.4.12:22:")

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadPort returns a port nothing is listening on.
func deadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// TestGenerateConsoleSample writes the console output quoted in
// docs/console-output.md, and the mid-transfer listing quoted in
// docs/transfer-lifecycle.md. Like the log sample it is skipped unless
// GOFT_LOG_SAMPLE names an output directory.
func TestGenerateConsoleSample(t *testing.T) {
	out := os.Getenv("GOFT_LOG_SAMPLE")
	if out == "" {
		t.Skip("set GOFT_LOG_SAMPLE to the output directory")
	}

	localDir := t.TempDir()
	remoteDir := t.TempDir()
	remote := sftptest.Start(t, remoteDir)

	write := func(name, body string) {
		p := filepath.Join(localDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfgPath := func(local string, capMB int, extra string) string {
		body := fmt.Sprintf(`
name: invoice-upload
local:
  path: %s
remote:
  protocol: sftp
  host: %s
  port: %d
  user: %s
  password: %s
  known_hosts: %s
  path: %s
  use_ssh_config: false
recursive: true
include: ["*.csv", "*.dat"]
max_file_size_mb: %d
stable_duration: 3s
workers: 2
verify: hash
%s
log:
  level: info
`, local, remote.Host, remote.Port, remote.User, string(remote.Password),
			remote.KnownHosts, remoteDir, capMB, extra)

		p := filepath.Join(t.TempDir(), "job.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// The console writes to os.Stdout, which is what a person sees, so it is
	// captured the way it is produced rather than through an injected writer.
	capture := func(fn func()) string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		saved := os.Stdout
		os.Stdout = w
		done := make(chan string, 1)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		fn()
		w.Close()
		os.Stdout = saved
		return <-done
	}

	save := func(name, body string, align bool) {
		p := filepath.Join(out, name)
		body = strings.ReplaceAll(body, localDir, "/data/out/invoice")
		body = strings.ReplaceAll(body, remoteDir, "/upload/invoice")
		body = strings.ReplaceAll(body, remote.Host, "invoice-sftp")
		body = regexp.MustCompile(`invoice-sftp:\d+`).ReplaceAllString(body, "invoice-sftp:22")
		body = regexp.MustCompile(`(?m)^(port +)\d+`).ReplaceAllString(body, "${1}22")
		body = strings.ReplaceAll(body, "tester", "uploader")
		body = regexp.MustCompile(`port = \d+`).ReplaceAllString(body, "port = 22")
		body = regexp.MustCompile(`/tmp/[^\s]*?/known_hosts`).
			ReplaceAllString(body, "/home/svc-transfer/.ssh/known_hosts")
		body = regexp.MustCompile(`/tmp/[^\s]*?/job\.yaml`).
			ReplaceAllString(body, "/etc/goft/invoice-upload.yaml")
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			body = strings.ReplaceAll(body, home, "/home/svc-transfer")
		}
		if align {
			body = realign(body)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("invoice_202608_01.csv", strings.Repeat("id,amount\n1,1200\n", 50000))
	write("invoice_202608_02.csv", strings.Repeat("id,amount\n2,980\n", 40000))
	write("archive.dat", strings.Repeat("x", 2<<20))
	write("2026-08/invoice_202608_03.csv", "id,amount\n3,1100\n")

	// What `goft test` reports before anything has been transferred.
	var testOut bytes.Buffer
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", cfgPath(localDir, 1, "")})
	rootCmd.SetOut(&testOut)
	Execute()
	save("goft-test.txt", testOut.String(), true)

	save("console-dry-run.txt", capture(func() {
		resetFlags()
		rootCmd.SetArgs([]string{"send", "-c", cfgPath(localDir, 1, ""), "--dry-run"})
		rootCmd.SetOut(os.Stdout)
		Execute()
	}), false)

	save("console-send.txt", capture(func() {
		resetFlags()
		rootCmd.SetArgs([]string{"send", "-c", cfgPath(localDir, 1, "on_exists: skip"), "--console"})
		rootCmd.SetOut(os.Stdout)
		Execute()
	}), false)

	// The same command again, with everything already delivered.
	save("console-send-again.txt", capture(func() {
		resetFlags()
		rootCmd.SetArgs([]string{"send", "-c", cfgPath(localDir, 1, "on_exists: skip"), "--console"})
		rootCmd.SetOut(os.Stdout)
		Execute()
	}), false)

	// A file big enough that the destination can be listed while it is still
	// being written, which is how the temporary name is shown rather than
	// described.
	bigDir := t.TempDir()
	big := filepath.Join(bigDir, "statement_202608.dat")
	if err := os.WriteFile(big, bytes.Repeat([]byte("statement line\n"), 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	bigCfg := cfgPath(bigDir, 0, "on_exists: overwrite")

	save("mid-transfer.txt", midTransferListing(t, remoteDir, func() {
		resetFlags()
		rootCmd.SetArgs([]string{"send", "-c", bigCfg, "--no-console"})
		rootCmd.SetOut(os.Stdout)
		Execute()
	}), false)
}

// midTransferListing runs a transfer and reports what the destination
// directory held while it was in progress, alongside what it holds once the
// transfer is over. This is how the temporary name is shown to be real rather
// than described.
func midTransferListing(t *testing.T, dir string, transfer func()) string {
	t.Helper()

	found := make(chan []string, 1)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				close(found)
				return
			default:
			}
			if names := listNames(dir); containsTemp(names) {
				found <- names
				close(found)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	transfer()
	close(stop)

	var b strings.Builder
	b.WriteString("$ ls -1 /upload/invoice        # while the transfer is running\n")
	if names, ok := <-found; ok {
		for _, n := range names {
			b.WriteString(n + "\n")
		}
	}
	b.WriteString("\n$ ls -1 /upload/invoice        # once it has finished\n")
	for _, n := range listNames(dir) {
		b.WriteString(n + "\n")
	}
	return b.String()
}

// realign re-pads the columns of a table whose values were rewritten, so that
// the sample reads the way the command's own output does.
func realign(text string) string {
	sep := regexp.MustCompile(` {2,}`)
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		rows = append(rows, sep.Split(strings.TrimRight(line, " "), -1))
	}

	var width []int
	for _, row := range rows {
		for i, cell := range row[:max(len(row)-1, 0)] {
			for len(width) <= i {
				width = append(width, 0)
			}
			width[i] = max(width[i], len(cell))
		}
	}

	var b strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			if i == len(row)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(cell + strings.Repeat(" ", width[i]-len(cell)+2))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func listNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func containsTemp(names []string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, ".goft.tmp") {
			return true
		}
	}
	return false
}

// TestGenerateLocalSample writes the output quoted in docs/local-copy.md: a
// copy between two directories on this machine, which needs no server at all.
func TestGenerateLocalSample(t *testing.T) {
	out := os.Getenv("GOFT_LOG_SAMPLE")
	if out == "" {
		t.Skip("set GOFT_LOG_SAMPLE to the output directory")
	}

	src := t.TempDir()
	dst := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "local-copy.log")

	for name, body := range map[string]string{
		"invoice_202608_01.csv":         strings.Repeat("id,amount\n1,1200\n", 50000),
		"2026-08/invoice_202608_02.csv": "id,amount\n2,980\n",
	} {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join(t.TempDir(), "job.yaml")
	body := fmt.Sprintf(`
name: invoice-archive
local:
  path: %s
remote:
  protocol: local
  path: %s
recursive: true
include: ["*.csv"]
stable_duration: 3s
workers: 2
verify: hash
on_exists: skip
log:
  path: %s
  level: info
`, src, dst, logFile)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	clean := func(s string) string {
		s = strings.ReplaceAll(s, src, "/data/out/invoice")
		s = strings.ReplaceAll(s, dst, "/backup/invoice")
		s = strings.ReplaceAll(s, cfg, "/etc/goft/invoice-archive.yaml")
		s = strings.ReplaceAll(s, logFile, "/var/log/goft/invoice-archive.log")
		return s
	}
	save := func(name, body string, align bool) {
		body = clean(body)
		if align {
			body = realign(body)
		}
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var testOut bytes.Buffer
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", cfg})
	rootCmd.SetOut(&testOut)
	Execute()
	save("local-test.txt", testOut.String(), true)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	resetFlags()
	rootCmd.SetArgs([]string{"send", "-c", cfg, "--console"})
	rootCmd.SetOut(os.Stdout)
	Execute()
	w.Close()
	os.Stdout = saved
	save("local-send.txt", <-done, false)

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	save("local-copy.log", string(log), false)
}
