package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/i18n"
)

// captureFile runs fn with *target (os.Stdout or os.Stderr) going to a pipe,
// and returns what was written there.
func captureFile(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := *target
	*target = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { *target = saved }()
	fn()
	w.Close()
	*target = saved
	return <-done
}

// run executes a command line and returns its exit code with what it wrote
// to stdout, through cobra or directly, and to stderr.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out bytes.Buffer
	stderr = captureFile(t, &os.Stderr, func() {
		direct := captureFile(t, &os.Stdout, func() {
			resetFlags()
			rootCmd.SetArgs(args)
			rootCmd.SetOut(&out)
			code = Execute()
			rootCmd.SetOut(os.Stdout)
		})
		out.WriteString(direct)
	})
	return code, out.String(), stderr
}

func TestHelpIsWrittenInTheLanguageAskedFor(t *testing.T) {
	_, ja, _ := run(t, "--lang", "ja", "--help")
	for _, want := range []string{"使い方:", "コマンド:", "フラグ:", "転送はせずに、設定と接続を確認する", "表示する言語", "goft のヘルプを表示する"} {
		if !strings.Contains(ja, want) {
			t.Errorf("Japanese help does not contain %q:\n%s", want, ja)
		}
	}

	// Asking again in English puts back every text the Japanese replaced.
	_, en, _ := run(t, "--lang", "en", "--help")
	for _, want := range []string{"Usage:", "Available Commands:", "Check the configuration and the connection without transferring anything", "help for goft"} {
		if !strings.Contains(en, want) {
			t.Errorf("English help does not contain %q:\n%s", want, en)
		}
	}
	if strings.ContainsAny(en, "使設") {
		t.Errorf("English help still has Japanese in it:\n%s", en)
	}
}

func TestSubcommandHelpIsTranslatedToo(t *testing.T) {
	_, out, _ := run(t, "--lang", "ja", "serve", "send", "--help")
	for _, want := range []string{"ローカルのディレクトリを監視して相手側へ転送する", "共通フラグ:", "send のヘルプを表示する", "転送はせずに、転送するファイルを一覧表示する"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not contain %q:\n%s", want, out)
		}
	}

	_, out, _ = run(t, "--lang", "ja", "help", "completion", "zsh")
	for _, want := range []string{"zsh 用の補完スクリプトを生成します。", "goft completion zsh", "補完候補の説明を表示しない"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion help does not contain %q:\n%s", want, out)
		}
	}

	_, out, _ = run(t, "--lang", "ja", "help", "help")
	if !strings.Contains(out, "任意のコマンドのヘルプを表示します。\n詳しくは goft help [コマンドのパス] と入力してください。") {
		t.Errorf("help of help is not in Japanese:\n%s", out)
	}
}

func TestTheLanguageComesFromGOFT_LANGWithoutTheFlag(t *testing.T) {
	t.Setenv(i18n.EnvLang, "ja")
	_, out, _ := run(t, "--help")
	if !strings.Contains(out, "使い方:") {
		t.Errorf("GOFT_LANG=ja did not choose Japanese:\n%s", out)
	}
}

func TestALanguageGoftDoesNotSpeakIsAnError(t *testing.T) {
	code, _, stderr := run(t, "--lang", "jp", "version")
	if code != 2 || !strings.Contains(stderr, `--lang: "jp" is not a language goft speaks (en | ja)`) {
		t.Errorf("exit %d, stderr %q; want 2 and the language refused", code, stderr)
	}
}

func TestVersionReadsTheSameInEveryLanguage(t *testing.T) {
	_, en, _ := run(t, "--lang", "en", "version")
	_, ja, _ := run(t, "--lang", "ja", "version")
	if en != ja || !strings.HasPrefix(en, "goft ") {
		t.Errorf("version differs by language: %q and %q", en, ja)
	}
}

func TestErrorsAreReportedInJapanese(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(cfg, []byte("local:\n  path: "+t.TempDir()+"\nremote:\n  protocol: sftp\n  path: /x\nworkers: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "--lang", "ja", "send", "-c", cfg)
	for _, want := range []string{"goft: 設定 " + cfg + " に誤りがあります:", "remote.host は必須です", "workers は 1 以上にしてください"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestJapaneseOnScreenAndEnglishInTheLog(t *testing.T) {
	s := newScenario(t, "")
	logPath := filepath.Join(t.TempDir(), "job.log")
	s.writeLocal("請求書.csv", "id,amount\n1,100\n")

	code, out, _ := run(t, "--lang", "ja", "send", "-c", s.cfgPath, "--console", "--log-file", logPath)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"（1 件、", "完了", "1 件: 転送 1（", "スキップ 0、失敗 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("console does not contain %q:\n%s", want, out)
		}
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"msg":"starting"`, `"msg":"transfer"`, `"msg":"cycle complete"`,
		`"msg":"stable_duration is 0: files that are still being written may be transferred"`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log does not contain %q:\n%s", want, log)
		}
	}
	if strings.Contains(string(log), "書き込み中") || strings.Contains(string(log), "完了") {
		t.Errorf("the log has Japanese in it beyond the file name:\n%s", log)
	}
}

func TestTheTestCommandSpeaksJapanese(t *testing.T) {
	s := newScenario(t, "")
	code, out, _ := run(t, "--lang", "ja", "test", "-c", s.cfgPath)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"設定", "ローカル", "読み書き可能", "解決", "host = 127.0.0.1", "(yaml)", "接続", "受信（一覧）", "0 件", "送信（書込）", "書き込み可能", "警告"} {
		if !strings.Contains(out, want) {
			t.Errorf("goft test does not contain %q:\n%s", want, out)
		}
	}
}
