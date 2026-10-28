//go:build !windows

package i18n

// systemLanguage is the language the system says the user reads, where there
// is such a thing apart from the locale. Elsewhere than Windows the locale is
// all there is.
var systemLanguage = func() string { return "" }
