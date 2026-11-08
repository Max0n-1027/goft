package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFile writes a file under dir and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// sendJob is a job's YAML, as an item of a jobs list.
func sendJob(name, local string) string {
	return "  - name: " + name + "\n    direction: send\n    local:\n      path: " + local +
		"\n    remote:\n      protocol: sftp\n      host: h\n      path: /" + name + "\n"
}

func TestJobFilesAddTheirJobsInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "jobs.d/b.yaml", "jobs:\n"+sendJob("b", "/b"))
	writeFile(t, dir, "jobs.d/a.yaml", "jobs:\n"+sendJob("a", "/a")+sendJob("a2", "/a2"))
	writeFile(t, dir, "extra.yaml", "jobs:\n"+sendJob("x", "/x"))
	main := writeFile(t, dir, "goft.yaml", "job_files:\n  - jobs.d/*.yaml\n  - extra.yaml\n  - jobs.d/a.yaml\njobs:\n"+sendJob("main", "/m"))

	cfg, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, j := range cfg.Jobs {
		names = append(names, j.Name)
	}
	// The main file's jobs, then each file's in the order the patterns give
	// them, a file named twice read once.
	if got := strings.Join(names, " "); got != "main a a2 b x" {
		t.Errorf("jobs = %s, want main a a2 b x", got)
	}
	if len(cfg.JobFiles) != 3 {
		t.Errorf("job files = %v, want three, each once", cfg.JobFiles)
	}
	if j, _ := cfg.Job("x"); j == nil || j.SourceFile != filepath.Join(dir, "extra.yaml") {
		t.Errorf("job x came from %v, want extra.yaml", j)
	}
}

func TestAPatternMayMatchNothingButANameMustExist(t *testing.T) {
	dir := t.TempDir()
	main := writeFile(t, dir, "goft.yaml", "job_files: [jobs.d/*.yaml]\njobs:\n"+sendJob("a", "/a"))
	if _, err := Load(main); err != nil {
		t.Errorf("Load() = %v, want an empty jobs.d accepted", err)
	}
	main = writeFile(t, dir, "goft.yaml", "job_files: [missing.yaml]\njobs:\n"+sendJob("a", "/a"))
	if _, err := Load(main); err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("Load() = %v, want the missing file named", err)
	}
}

func TestAJobFileHoldsJobsAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "more.yaml", "service:\n  lock_dir: /x\njobs:\n"+sendJob("a", "/a"))
	main := writeFile(t, dir, "goft.yaml", "job_files: [more.yaml]\n")
	if _, err := Load(main); err == nil || !strings.Contains(err.Error(), "service") {
		t.Errorf("Load() = %v, want the service section in a job file refused", err)
	}
}

func TestAnOldJobFileIsRecognised(t *testing.T) {
	main := writeFile(t, t.TempDir(), "job.yaml", "name: x\nlocal:\n  path: /a\nremote:\n  protocol: sftp\n  host: h\n  path: /p\n")
	_, err := Load(main)
	if err == nil || !strings.Contains(err.Error(), "move its settings under jobs:") {
		t.Errorf("Load() = %v, want the old shape recognised and the fix named", err)
	}
}

func TestUnknownTopLevelKeysAreRefused(t *testing.T) {
	main := writeFile(t, t.TempDir(), "goft.yaml", "job:\n"+sendJob("a", "/a"))
	if _, err := Load(main); err == nil || !strings.Contains(err.Error(), "job is not a setting here") {
		t.Errorf("Load() = %v, want the misspelt key named", err)
	}
}

func TestMalformedSectionsAreReported(t *testing.T) {
	for name, body := range map[string]string{
		"jobs not a list":      "jobs: x\n",
		"job not a map":        "jobs:\n  - x\n",
		"service not a map":    "service: x\njobs:\n" + sendJob("a", "/a"),
		"job_files not a list": "job_files: {a: b}\n",
		"job_files of numbers": "job_files: [1]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFile(t, t.TempDir(), "goft.yaml", body)); err == nil {
				t.Error("Load() = nil, want the malformed section reported")
			}
		})
	}
}

func TestServiceSettingsResolveAgainstTheMainFile(t *testing.T) {
	dir := t.TempDir()
	main := writeFile(t, dir, "goft.yaml", "service:\n  log:\n    path: logs/goft.log\n  lock_dir: locks\njobs:\n"+sendJob("a", "/a"))
	cfg, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Service.Log.Path != filepath.Join(dir, "logs", "goft.log") || cfg.Service.LockDir != filepath.Join(dir, "locks") {
		t.Errorf("service = %+v, want its paths under %s", cfg.Service, dir)
	}
	if cfg.Service.Log.Level != "info" || cfg.Service.Log.Rotation != RotationSize {
		t.Errorf("service log = %+v, want the defaults filled in", cfg.Service.Log)
	}
}

func TestEnvironmentReachesIntoTheJobsList(t *testing.T) {
	t.Setenv("GOFT_TEST_SECRET", "s3cret")
	main := writeFile(t, t.TempDir(), "goft.yaml",
		"jobs:\n"+sendJob("a", "/a")+"    include: [\"${GOFT_TEST_SECRET}.csv\"]\n")
	cfg, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Jobs[0].Include; len(got) != 1 || got[0] != "s3cret.csv" {
		t.Errorf("include = %v, want the variable expanded inside the list", got)
	}
}

func TestAPathInAJobFileIsRelativeToThatFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "jobs.d/a.yaml", "jobs:\n"+sendJob("a", "out"))
	main := writeFile(t, dir, "goft.yaml", "job_files: [jobs.d/a.yaml]\n")
	cfg, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Jobs[0].Local.Path; got != filepath.Join(dir, "jobs.d", "out") {
		t.Errorf("local.path = %s, want it under jobs.d", got)
	}
}

func TestEnabledAndJob(t *testing.T) {
	off := validJob("/b")
	off.Name, off.Enabled = "b", false
	on := validJob("/a")
	on.Name = "a"
	cfg := &Config{Jobs: []*Job{on, off}}
	if got := cfg.Enabled(); len(got) != 1 || got[0] != on {
		t.Errorf("Enabled() = %v, want only a", got)
	}
	if j, ok := cfg.Job("b"); !ok || j != off {
		t.Error("Job(b) did not find the disabled job")
	}
	if _, ok := cfg.Job("c"); ok {
		t.Error("Job(c) found a job that is not there")
	}
}

// configOf builds a valid configuration of jobs for the checks across them.
func configOf(jobs ...*Job) *Config {
	return &Config{
		SourceFile: "goft.yaml",
		Service:    Service{Log: Log{Level: "info", Rotation: RotationSize}},
		Jobs:       jobs,
	}
}

func TestValidateNeedsAJob(t *testing.T) {
	if err := Validate(configOf()); err == nil || !strings.Contains(err.Error(), "no jobs") {
		t.Errorf("Validate() = %v, want an empty configuration refused", err)
	}
}

func TestValidateNamesTheJobAProblemBelongsTo(t *testing.T) {
	a := validJob(t.TempDir())
	a.Name, a.Workers = "invoice", 0
	b := validJob(t.TempDir())
	b.Name, b.Direction = "", ""
	err := Validate(configOf(a, b))
	for _, want := range []string{`job invoice: workers must be >= 1`, `job #2: name is required`, `job #2: direction is required`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %v, want %q", err, want)
		}
	}
}

func TestValidateRefusesABadDirectionAndName(t *testing.T) {
	j := validJob(t.TempDir())
	j.Direction = "upload"
	j.Name = "a\tb"
	err := validate(j)
	if err == nil || !strings.Contains(err.Error(), `direction "upload" is invalid`) || !strings.Contains(err.Error(), "control character") {
		t.Errorf("validate() = %v, want the direction and the name refused", err)
	}
}

func TestValidateRefusesDuplicateNames(t *testing.T) {
	a, b := validJob(t.TempDir()), validJob(t.TempDir())
	a.Name, b.Name = "Invoice", "invoice"
	a.SourceFile, b.SourceFile = "/etc/goft/goft.yaml", "/etc/goft/jobs.d/b.yaml"
	b.Remote.Path = "/other"
	err := Validate(configOf(a, b))
	if err == nil || !strings.Contains(err.Error(), `job "invoice" in /etc/goft/jobs.d/b.yaml has the same name as job "Invoice" in /etc/goft/goft.yaml`) {
		t.Errorf("Validate() = %v, want names differing only in case refused", err)
	}
}

func TestValidateRefusesTwoJobsTakingFromTheSameSource(t *testing.T) {
	src := t.TempDir()
	a, b := validJob(src), validJob(filepath.Join(src, "sub"))
	if err := os.Mkdir(b.Local.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	a.Name, b.Name = "a", "b"
	b.Remote.Path = "/other"

	// Two readers are fine: neither takes anything away.
	if err := Validate(configOf(a, b)); err != nil {
		t.Fatalf("Validate() = %v, want two readers of one directory accepted", err)
	}
	a.Recursive = true
	a.PostAction = PostDelete
	if err := Validate(configOf(a, b)); err == nil || !strings.Contains(err.Error(), "both take files from") {
		t.Errorf("Validate() = %v, want a deleting job sharing its source refused", err)
	}
	// A job the service does not run cannot race the one it does.
	b.Enabled = false
	if err := Validate(configOf(a, b)); err != nil {
		t.Errorf("Validate() = %v, want a disabled job left out of the check", err)
	}
}

func TestValidateRefusesTwoRecvJobsDeletingFromOneServerDirectory(t *testing.T) {
	a, b := validJob(t.TempDir()), validJob(t.TempDir())
	a.Name, b.Name = "a", "b"
	for _, j := range []*Job{a, b} {
		j.Direction, j.PostAction = DirRecv, PostDelete
		j.Remote.Port = 22 // written out for one, left to the default for the other
	}
	b.Remote.Port = 0
	a.Remote.Host, b.Remote.Host = "h", "H"
	if err := Validate(configOf(a, b)); err == nil || !strings.Contains(err.Error(), "sftp://h:22/upload") {
		t.Errorf("Validate() = %v, want the shared server directory found", err)
	}
}

func TestValidateRefusesALoopButNotARelay(t *testing.T) {
	local, staging := t.TempDir(), t.TempDir()
	up := validJob(local)
	up.Name = "up"
	down := validJob(local)
	down.Name, down.Direction = "down", DirRecv

	// up sends local to sftp://example/upload, down brings it back.
	if err := Validate(configOf(up, down)); err == nil || !strings.Contains(err.Error(), "up -> down -> up") {
		t.Errorf("Validate() = %v, want the loop named", err)
	}

	// down into a staging directory that another job sends on is a relay.
	down.Local.Path = staging
	relay := validJob(staging)
	relay.Name = "relay"
	relay.Remote.Host = "elsewhere"
	if err := Validate(configOf(up, down, relay)); err != nil {
		t.Errorf("Validate() = %v, want a relay accepted", err)
	}
}

func TestValidateRefusesOneLogFileRotatedTwoWays(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "goft.log")
	a, b := validJob(t.TempDir()), validJob(t.TempDir())
	a.Name, b.Name = "a", "b"
	b.Remote.Path = "/other"
	a.Log.Path, b.Log.Path = logPath, logPath
	if err := Validate(configOf(a, b)); err != nil {
		t.Fatalf("Validate() = %v, want one file with one rotation accepted", err)
	}
	b.Log.Rotation = RotationDaily
	cfg := configOf(a, b)
	cfg.Service.Log.Path = logPath
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "different rotation settings") {
		t.Errorf("Validate() = %v, want the mismatch refused", err)
	}
}

func TestValidateChecksTheServiceLog(t *testing.T) {
	cfg := configOf(validJob(t.TempDir()))
	cfg.Service.Log.Rotation = "weekly"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), `service.log.rotation "weekly" is invalid`) {
		t.Errorf("Validate() = %v, want the service log checked", err)
	}
}

func TestLocationsDescribeThemselves(t *testing.T) {
	if got := (location{dir: "/data"}).String(); got != "/data" {
		t.Errorf("local location = %s", got)
	}
	r := Remote{Protocol: ProtocolSMB, Host: "FS", Share: "Docs", Path: "in"}
	if got := remoteLocation(r).String(); got != "smb://fs:445/docs/in" {
		t.Errorf("smb location = %s", got)
	}
	if !remoteLocation(Remote{Protocol: ProtocolFTP, Host: "h", Path: "/"}).overlaps(remoteLocation(Remote{Protocol: ProtocolFTP, Host: "h", Port: 21, Path: "/x"})) {
		t.Error("the root of a server should overlap everything on it")
	}
}

func TestOnlyPathsRelativeToTheWorkingDirectoryAreResolved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conf")
	cases := map[string]string{
		"":                             "",
		"in":                           filepath.Join(dir, "in"),
		filepath.Join("a", "b"):        filepath.Join(dir, "a", "b"),
		"~/.ssh/id_ed25519":            "~/.ssh/id_ed25519",
		"%d/.ssh/known_hosts":          "%d/.ssh/known_hosts",
		"/run/agent.sock":              "/run/agent.sock",
		`\\.\pipe\openssh-ssh-agent`:   `\\.\pipe\openssh-ssh-agent`,
		filepath.Join(dir, "absolute"): filepath.Join(dir, "absolute"),
	}
	if runtime.GOOS == "windows" {
		// Relative to the root of the current drive, and to a drive's own
		// working directory: neither is anything under the configuration.
		cases[`\upload`] = `\upload`
		cases[`C:data`] = `C:data`
	}
	for in, want := range cases {
		if got := under(dir, in); got != want {
			t.Errorf("under(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAJobIsFoundUnderItsNameInAnyCase(t *testing.T) {
	a := validJob(t.TempDir())
	a.Name = "Invoice-Upload"
	c := configOf(a)
	for _, name := range []string{"Invoice-Upload", "invoice-upload", "INVOICE-UPLOAD"} {
		if j, ok := c.Job(name); !ok || j != a {
			t.Errorf("Job(%q) = %v, %v; want the job", name, j, ok)
		}
	}
	if _, ok := c.Job("invoice"); ok {
		t.Error("Job found a job by part of its name")
	}
}

func TestPathKeySharesEverySpellingOfOneFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	same := []string{"jobs.log", filepath.Join(dir, "jobs.log"), filepath.Join(dir, "x", "..", "jobs.log")}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		same = append(same, "JOBS.log", strings.ToUpper(filepath.Join(dir, "jobs.log")))
	}
	for _, p := range same {
		if PathKey(p) != PathKey(same[0]) {
			t.Errorf("PathKey(%q) = %q, want %q", p, PathKey(p), PathKey(same[0]))
		}
	}
	if runtime.GOOS == "linux" && PathKey("JOBS.log") == PathKey("jobs.log") {
		t.Error("PathKey folded case where file names keep it")
	}
}

func TestOneLogFileUnderTwoSpellingsIsOneFile(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("file names keep their case here, so the two spellings are two files")
	}
	logs := t.TempDir()
	a, b := validJob(t.TempDir()), validJob(t.TempDir())
	a.Name, b.Name = "a", "b"
	b.Remote.Path = "/other"
	a.Log.Path = filepath.Join(logs, "jobs.log")
	b.Log.Path = filepath.Join(logs, "JOBS.log")
	b.Log.Rotation = RotationDaily
	if err := Validate(configOf(a, b)); err == nil || !strings.Contains(err.Error(), "different rotation settings") {
		t.Errorf("Validate() = %v, want one file rotated two ways refused", err)
	}
}

func TestAJobFileInTheOldShapeSaysHowToConvertIt(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "goft.yaml")
	if err := os.WriteFile(main, []byte("job_files: [old.yaml]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := "name: old\nlocal:\n  path: " + t.TempDir() + "\nremote:\n  protocol: local\n  path: " + t.TempDir() + "\n"
	if err := os.WriteFile(filepath.Join(dir, "old.yaml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(main)
	if err == nil || !strings.Contains(err.Error(), "old.yaml is a job file from before goft ran several jobs at once") {
		t.Errorf("Load() = %v, want the old shape recognised in a job file", err)
	}
}
