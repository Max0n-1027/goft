package i18n

import "golang.org/x/sys/windows"

// systemLanguage is the user's display language, such as ja-JP. A console on
// Windows sets no locale variables, so this is what decides there unless
// GOFT_LANG or --lang does.
var systemLanguage = func() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
