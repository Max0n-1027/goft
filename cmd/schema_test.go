package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/schema"
)

func TestSchemaPrintsTheSchemaGoftChecksWith(t *testing.T) {
	code, out, _ := run(t, "schema")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out != schema.JSON {
		t.Error("goft schema printed something other than the embedded schema")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("goft schema printed something that is not JSON: %v", err)
	}
	if doc["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("$schema = %v, want draft-07", doc["$schema"])
	}
}

func TestSchemaIsDescribedInTheLanguageAskedFor(t *testing.T) {
	_, out, _ := run(t, "--lang", "ja", "schema", "--help")
	if !strings.Contains(out, "設定ファイルの JSON Schema（draft-07）を出力します。") {
		t.Errorf("help is not in Japanese:\n%s", out)
	}
}

func TestAConfigurationBreakingTheSchemaIsRefusedWithItsLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goft.yaml")
	body := jobYAML("local: {path: /a}\nremote: {protocol: local, path: /b}\npost_acton: delete\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "--lang", "en", "-c", path, "jobs")
	want := "goft: invalid configuration " + path + ":\nline 6: job job: post_acton is not a setting (did you mean post_action?)\n"
	if code != 2 || stderr != want {
		t.Errorf("exit %d, want 2 and the misspelt setting named on its line:\n%s", code, stderr)
	}

	code, _, stderr = run(t, "--lang", "ja", "-c", path, "jobs")
	if code != 2 || !strings.Contains(stderr, "6 行目: ジョブ job: post_acton という設定はありません（post_action の書き間違いではありませんか？）") {
		t.Errorf("exit %d, want the same in Japanese:\n%s", code, stderr)
	}
}
