// Package i18n renders what goft says to a person in English or Japanese,
// while everything it writes to its log stays in English.
//
// Text is keyed by its English wording, written as a format string, so the
// English output is the key itself and code reads as it always did. A
// translation that is missing falls back to the English rather than to a
// placeholder, and a test over the whole module makes sure none is missing.
//
// Errors and warnings are made where the language is not known — deep in a
// connection, while validating a file — and are shown somewhere it is: on the
// console, by goft test, on stderr. They are also logged, in English. So they
// are built with [Errorf] and [Msg], which remember the format and the
// arguments: Error and String give the English, as fmt.Errorf would, and
// [Printer.Error] and [Message.Localize] render the same text in the reader's
// language. An error from the operating system or a library has no
// translation and is shown as it is, inside a translated sentence where goft
// wrapped it in one.
//
// The catalog is a map consulted by [Printer.Sprintf], which formats with fmt.
// golang.org/x/text/message would also select plural forms, which neither
// language needs here, and it formats numbers for the locale: a port or a byte
// count would come out with thousands separators on the console while the log
// beside it had none.
package i18n

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Printer renders text in one language.
type Printer struct {
	lang    Lang
	catalog map[string]string
}

// Lang is a language goft speaks.
type Lang string

// The languages there are translations for. English is the source, so it has
// no catalog of its own.
const (
	LangEnglish  Lang = "en"
	LangJapanese Lang = "ja"
)

// Printers for each language.
var (
	English  = &Printer{lang: LangEnglish}
	Japanese = &Printer{lang: LangJapanese, catalog: japanese}
)

// For returns the printer for lang, or English for a language goft does not
// speak.
func For(lang Lang) *Printer {
	if lang == LangJapanese {
		return Japanese
	}
	return English
}

// Lang reports which language p renders.
func (p *Printer) Lang() Lang { return p.lang }

// Text returns key in p's language. It is for text that takes no arguments,
// such as a command's description; anything with a verb goes through Sprintf.
func (p *Printer) Text(key string) string {
	if p == nil || p.catalog == nil {
		return key
	}
	if t, ok := p.catalog[key]; ok {
		return t
	}
	return key
}

// Sprintf formats key, translated into p's language, with args. An argument
// that is itself translatable — a [Message], or an error made by [Errorf] — is
// rendered in the same language, so a translated sentence does not carry an
// English one inside it.
//
// %w is accepted and treated as %v, so the format given to Errorf can be
// shared with it.
func (p *Printer) Sprintf(key string, args ...any) string {
	return fmt.Sprintf(withoutW(p.Text(key)), p.localizeArgs(args)...)
}

// wrapVerb finds %w, with an explicit argument index or without.
var wrapVerb = regexp.MustCompile(`%(\[\d+\])?w`)

// withoutW turns each %w into %v, which is what fmt.Errorf prints it as; only
// Errorf takes %w itself.
func withoutW(format string) string {
	return wrapVerb.ReplaceAllString(format, "%${1}v")
}

func (p *Printer) localizeArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case Localizer:
			out[i] = v.Localize(p)
		case error:
			out[i] = p.Error(v)
		default:
			out[i] = a
		}
	}
	return out
}

// Error renders err in p's language, as far as it can be: the parts goft
// wrote are translated, and what the operating system or a library said is
// left as it said it. In English it is exactly err.Error().
func (p *Printer) Error(err error) string {
	switch {
	case err == nil:
		return ""
	case p == nil || p.catalog == nil:
		return err.Error()
	}
	if l, ok := err.(Localizer); ok {
		return l.Localize(p)
	}

	// errors.Join puts one error on each line; translate each line.
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts := j.Unwrap()
		english := make([]string, len(parts))
		for i, e := range parts {
			english[i] = e.Error()
		}
		if err.Error() == strings.Join(english, "\n") {
			local := make([]string, len(parts))
			for i, e := range parts {
				local[i] = p.Error(e)
			}
			return strings.Join(local, "\n")
		}
	}

	// A wrapper that adds nothing to the text — one that only carries an exit
	// code, say — is seen through to what it wraps.
	if inner := errors.Unwrap(err); inner != nil && inner.Error() == err.Error() {
		return p.Error(inner)
	}
	return err.Error()
}

// Localizer is text that can be rendered in a reader's language.
type Localizer interface {
	Localize(p *Printer) string
}

// Message is text decided now and shown later, in whichever language the
// reader turns out to want. String renders it in English, which is what the
// log records.
type Message struct {
	key  string
	args []any
}

// Msg makes a Message from a format string in English and its arguments.
func Msg(key string, args ...any) Message { return Message{key: key, args: args} }

// String renders m in English.
func (m Message) String() string { return English.Sprintf(m.key, m.args...) }

// Localize renders m in p's language.
func (m Message) Localize(p *Printer) string { return p.Sprintf(m.key, m.args...) }

// IsZero reports whether m holds no text at all.
func (m Message) IsZero() bool { return m.key == "" }

// N marks text for translation without translating it, for text fixed before
// the reader's language is known, such as a command's description. It returns
// s as it is; [Printer.Text] translates it when it is shown. The mark is what
// lets the catalog test find the text.
func N(s string) string { return s }

// Error is an error whose message can be rendered in more than one language.
// Its Error method gives the English, exactly as fmt.Errorf would have, and it
// wraps what fmt.Errorf would have wrapped, so errors.Is and errors.As see
// through it the same way.
type Error struct {
	english error
	msg     Message
}

// Errorf is fmt.Errorf whose message can also be rendered in the reader's
// language. The format is the English text and the key to its translation.
func Errorf(key string, args ...any) error {
	return &Error{english: fmt.Errorf(key, args...), msg: Msg(key, args...)}
}

// Error returns the message in English.
func (e *Error) Error() string { return e.english.Error() }

// Unwrap returns the errors the format wrapped with %w.
func (e *Error) Unwrap() []error {
	switch u := e.english.(type) {
	case interface{ Unwrap() []error }:
		return u.Unwrap()
	case interface{ Unwrap() error }:
		if inner := u.Unwrap(); inner != nil {
			return []error{inner}
		}
	}
	return nil
}

// Localize renders the message in p's language.
func (e *Error) Localize(p *Printer) string { return e.msg.Localize(p) }
