package i18n

import (
	"strings"

	"golang.org/x/text/language"
)

// EnvLang names the environment variable that chooses the language for goft
// alone, ahead of the locale.
const EnvLang = "GOFT_LANG"

// Detect decides which language to speak.
//
// The first of these that says anything decides: the --lang flag, then
// GOFT_LANG, then the locale the environment asks for — LANGUAGE, LC_ALL,
// LC_MESSAGES and LANG, read as gettext reads them — and finally, on Windows,
// the user's display language. Japanese is spoken when that answer is
// Japanese, and English otherwise.
//
// flag and GOFT_LANG must name a language goft speaks, en or ja, because a
// person typed them for goft; a mistake there is reported rather than quietly
// answered in English. The locale is the whole system's and may name any
// language at all.
func Detect(flag string, getenv func(string) string) (*Printer, error) {
	if flag != "" {
		lang, err := choose(flag)
		if err != nil {
			return English, Errorf("--lang: %w", err)
		}
		return For(lang), nil
	}
	if v := getenv(EnvLang); v != "" {
		lang, err := choose(v)
		if err != nil {
			return English, Errorf("GOFT_LANG: %w", err)
		}
		return For(lang), nil
	}
	if lang, ok := fromLocale(getenv); ok {
		return For(lang), nil
	}
	if lang, ok := fromName(systemLanguage()); ok {
		return For(lang), nil
	}
	return English, nil
}

// choose reads a language a person named for goft.
func choose(name string) (Lang, error) {
	switch Lang(strings.ToLower(strings.TrimSpace(name))) {
	case LangEnglish:
		return LangEnglish, nil
	case LangJapanese:
		return LangJapanese, nil
	}
	return "", Errorf("%q is not a language goft speaks (%s | %s)", name, LangEnglish, LangJapanese)
}

// fromLocale reads the language out of the locale variables, the way gettext
// does: the locale is the first of LC_ALL, LC_MESSAGES and LANG that is set,
// and LANGUAGE, a list of languages in order of preference, comes before it
// unless the locale is C, which turns translation off.
func fromLocale(getenv func(string) string) (Lang, bool) {
	locale := ""
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := getenv(key); v != "" {
			locale = v
			break
		}
	}
	if locale == "C" || locale == "POSIX" || strings.HasPrefix(locale, "C.") {
		return LangEnglish, true
	}
	if list := getenv("LANGUAGE"); list != "" && locale != "" {
		for _, name := range strings.Split(list, ":") {
			if lang, ok := fromName(name); ok && lang != "" {
				return lang, true
			}
		}
	}
	if locale == "" {
		return "", false
	}
	if lang, ok := fromName(locale); ok {
		return lang, true
	}
	// A locale in a language goft does not speak still says something: that
	// the person does not read Japanese.
	return LangEnglish, true
}

// fromName reads a locale or language name — ja_JP.UTF-8, ja-JP, en_US,
// ja@modifier — and reports which of goft's languages it is, if either.
func fromName(name string) (Lang, bool) {
	name = strings.TrimSpace(name)
	if i := strings.IndexAny(name, ".@"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", false
	}
	tag, err := language.Parse(strings.ReplaceAll(name, "_", "-"))
	if err != nil {
		return "", false
	}
	base, _ := tag.Base()
	switch base.String() {
	case "ja":
		return LangJapanese, true
	case "en":
		return LangEnglish, true
	}
	return "", false
}

// String names the language as --lang and GOFT_LANG take it.
func (l Lang) String() string { return string(l) }
