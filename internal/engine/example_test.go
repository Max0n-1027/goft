package engine_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net/textproto"

	"goft/internal/engine"
)

// Retryable is what keeps a retry from being wasted effort: only failures that
// could go differently next time are worth another attempt.
//
// Each case is labelled rather than printed, because the text an error renders
// itself as belongs to the package that defines it and has changed under us
// before: net/textproto now quotes the reply message where it used to pass it
// through. What the classification turns on is the error's identity, not its
// wording, so that is what the example shows.
func ExampleRetryable() {
	for _, c := range []struct {
		what string
		err  error
	}{
		{"a dropped connection", errors.New("read: connection reset by peer")},
		{"a hash mismatch", errors.New("hash mismatch: sent 0001, destination has 0002")},
		{"a permission error", fmt.Errorf("create: %w", fs.ErrPermission)},
		{"a 5xx ftp reply", &textproto.Error{Code: 553, Msg: "Could not create file."}},
		{"a name the destination cannot hold", fmt.Errorf("%q: %w", "a:b.csv", fs.ErrInvalid)},
	} {
		fmt.Printf("%-35s %v\n", c.what, engine.Retryable(c.err))
	}
	// Output:
	// a dropped connection                true
	// a hash mismatch                     true
	// a permission error                  false
	// a 5xx ftp reply                     false
	// a name the destination cannot hold  false
}
