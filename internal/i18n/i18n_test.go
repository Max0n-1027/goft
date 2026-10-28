package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// withCatalog swaps in a small catalog, so that what is being tested is the
// mechanism rather than any one translation.
func withCatalog(t *testing.T, entries map[string]string) *Printer {
	t.Helper()
	return &Printer{lang: LangJapanese, catalog: entries}
}

func TestTextFallsBackToTheEnglish(t *testing.T) {
	p := withCatalog(t, map[string]string{"hello": "こんにちは"})
	if got := p.Text("hello"); got != "こんにちは" {
		t.Errorf("Text(hello) = %q", got)
	}
	if got := p.Text("not translated"); got != "not translated" {
		t.Errorf("Text of a missing key = %q, want the key", got)
	}
	if got := English.Text("hello"); got != "hello" {
		t.Errorf("English.Text = %q, want the key", got)
	}
	var none *Printer
	if got := none.Text("hello"); got != "hello" {
		t.Errorf("a nil printer's Text = %q, want the key", got)
	}
}

func TestSprintfTranslatesTheArgumentsToo(t *testing.T) {
	p := withCatalog(t, map[string]string{
		"outer %s: %w":     "外側 %s: %w",
		"inner %d":         "内側 %d",
		"note":             "注記",
		"%[1]s then %[2]s": "%[2]s の後に %[1]s",
	})
	inner := Errorf("inner %d", 7)
	got := p.Sprintf("outer %s: %w", Msg("note"), inner)
	if got != "外側 注記: 内側 7" {
		t.Errorf("Sprintf = %q", got)
	}
	if got := p.Sprintf("%[1]s then %[2]s", "a", "b"); got != "b の後に a" {
		t.Errorf("Sprintf with indexes = %q", got)
	}
}

func TestErrorfIsFmtErrorfInEnglish(t *testing.T) {
	cause := fmt.Errorf("open x: %w", fs.ErrNotExist)
	err := Errorf("read config %s: %w", "job.yaml", cause)
	want := fmt.Errorf("read config %s: %w", "job.yaml", cause)

	if err.Error() != want.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), want.Error())
	}
	if !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, cause) {
		t.Error("errors.Is does not see through the error to what it wraps")
	}
	var pathErr *fs.PathError
	if errors.As(Errorf("x: %w", &fs.PathError{Op: "open", Path: "p", Err: fs.ErrPermission}), &pathErr) == false {
		t.Error("errors.As does not see through the error")
	}
	if English.Error(err) != err.Error() {
		t.Errorf("English.Error = %q, want exactly Error()", English.Error(err))
	}
}

func TestErrorfWrapsEveryW(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	err := Errorf("%w and %w", a, b)
	if !errors.Is(err, a) || !errors.Is(err, b) {
		t.Error("both wrapped errors should be found")
	}
	if Errorf("no wrap").(*Error).Unwrap() != nil {
		t.Error("an error that wraps nothing should unwrap to nothing")
	}
}

func TestErrorTranslatesWhatGoftWrote(t *testing.T) {
	p := withCatalog(t, map[string]string{
		"dial %s: %w":          "%s に接続できません: %w",
		"the connection is %s": "接続は %s",
		"first":                "一つ目",
		"second":               "二つ目",
	})
	osErr := &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrPermission}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"an error goft wrote around one it did not", Errorf("dial %s: %w", "h:22", osErr), "h:22 に接続できません: open /x: permission denied"},
		{"an error from elsewhere", osErr, "open /x: permission denied"},
		{"errors.Join, one line each", errors.Join(Errorf("first"), Errorf("second")), "一つ目\n二つ目"},
		{"a wrapper that adds nothing", passThrough{Errorf("first")}, "一つ目"},
		{"a wrapper that adds text it cannot translate", fmt.Errorf("ctx: %w", Errorf("first")), "ctx: first"},
		{"a message argument", Errorf("the connection is %s", Msg("first")), "接続は 一つ目"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Error(tt.err); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// passThrough carries an error without adding to its text, as an exit code
// wrapper does.
type passThrough struct{ err error }

func (p passThrough) Error() string { return p.err.Error() }
func (p passThrough) Unwrap() error { return p.err }

func TestAMessageIsEnglishUntilLocalized(t *testing.T) {
	p := withCatalog(t, map[string]string{"%d keys": "鍵 %d 本"})
	m := Msg("%d keys", 3)
	if m.String() != "3 keys" {
		t.Errorf("String() = %q", m.String())
	}
	if got := m.Localize(p); got != "鍵 3 本" {
		t.Errorf("Localize = %q", got)
	}
	if m.IsZero() || !(Message{}).IsZero() {
		t.Error("IsZero is wrong")
	}
	if got := fmt.Sprint(m); got != "3 keys" {
		t.Errorf("a message printed with fmt = %q, want the English", got)
	}
}

func TestNLeavesTheTextAlone(t *testing.T) {
	if N("as written") != "as written" {
		t.Error("N must return its argument")
	}
}

func TestForAndLang(t *testing.T) {
	if For(LangJapanese) != Japanese || For(LangEnglish) != English || For("fr") != English {
		t.Error("For picks the wrong printer")
	}
	if Japanese.Lang() != LangJapanese || English.Lang().String() != "en" {
		t.Error("Lang is wrong")
	}
}

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestDetect(t *testing.T) {
	saved := systemLanguage
	t.Cleanup(func() { systemLanguage = saved })

	tests := []struct {
		name   string
		flag   string
		vars   map[string]string
		system string
		want   Lang
	}{
		{"nothing set", "", nil, "", LangEnglish},
		{"the flag first", "ja", map[string]string{EnvLang: "en", "LANG": "en_US.UTF-8"}, "", LangJapanese},
		{"the flag, written in capitals", "EN", map[string]string{"LANG": "ja_JP.UTF-8"}, "", LangEnglish},
		{"GOFT_LANG before the locale", "", map[string]string{EnvLang: "ja", "LC_ALL": "en_US.UTF-8"}, "", LangJapanese},
		{"LANG", "", map[string]string{"LANG": "ja_JP.UTF-8"}, "", LangJapanese},
		{"LANG with a modifier", "", map[string]string{"LANG": "ja_JP@euro"}, "", LangJapanese},
		{"LC_MESSAGES before LANG", "", map[string]string{"LC_MESSAGES": "en_US.UTF-8", "LANG": "ja_JP.UTF-8"}, "", LangEnglish},
		{"LC_ALL before LC_MESSAGES", "", map[string]string{"LC_ALL": "ja_JP.UTF-8", "LC_MESSAGES": "en_US.UTF-8"}, "", LangJapanese},
		{"LANGUAGE before the locale", "", map[string]string{"LANGUAGE": "fr:ja:en", "LANG": "en_US.UTF-8"}, "", LangJapanese},
		{"LANGUAGE naming nothing goft speaks", "", map[string]string{"LANGUAGE": "fr:de", "LANG": "ja_JP.UTF-8"}, "", LangJapanese},
		{"LANGUAGE ignored under C", "", map[string]string{"LANGUAGE": "ja", "LC_ALL": "C"}, "", LangEnglish},
		{"LANGUAGE ignored with no locale", "", map[string]string{"LANGUAGE": "ja"}, "", LangEnglish},
		{"C.UTF-8", "", map[string]string{"LANG": "C.UTF-8"}, "ja-JP", LangEnglish},
		{"POSIX", "", map[string]string{"LANG": "POSIX"}, "ja-JP", LangEnglish},
		{"a language goft does not speak", "", map[string]string{"LANG": "fr_FR.UTF-8"}, "ja-JP", LangEnglish},
		{"the system when no locale is set", "", nil, "ja-JP", LangJapanese},
		{"the system in another language", "", nil, "de-DE", LangEnglish},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			systemLanguage = func() string { return tt.system }
			p, err := Detect(tt.flag, env(tt.vars))
			if err != nil {
				t.Fatal(err)
			}
			if p.Lang() != tt.want {
				t.Errorf("Detect() = %s, want %s", p.Lang(), tt.want)
			}
		})
	}
}

func TestDetectRefusesALanguageItWasToldButDoesNotSpeak(t *testing.T) {
	p, err := Detect("jp", env(nil))
	if err == nil || !strings.Contains(err.Error(), `--lang: "jp" is not a language goft speaks (en | ja)`) {
		t.Errorf("Detect(--lang jp) = %v", err)
	}
	if p != English {
		t.Error("the error should be reported in English")
	}
	_, err = Detect("", env(map[string]string{EnvLang: "japanese"}))
	if err == nil || !strings.HasPrefix(err.Error(), "GOFT_LANG: ") {
		t.Errorf("Detect(GOFT_LANG=japanese) = %v", err)
	}
}

func TestFromName(t *testing.T) {
	for name, want := range map[string]Lang{
		"ja":          LangJapanese,
		"ja-JP":       LangJapanese,
		"ja_JP.eucJP": LangJapanese,
		"en_GB":       LangEnglish,
		"fr":          "",
		"":            "",
		"C":           "",
		"not a tag!":  "",
	} {
		got, _ := fromName(name)
		if got != want {
			t.Errorf("fromName(%q) = %q, want %q", name, got, want)
		}
	}
}
