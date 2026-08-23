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
func ExampleRetryable() {
	for _, err := range []error{
		errors.New("read: connection reset by peer"),
		fmt.Errorf("hash mismatch: sent 0001, destination has 0002"),
		fmt.Errorf("create: %w", fs.ErrPermission),
		&textproto.Error{Code: 553, Msg: "Could not create file."},
	} {
		fmt.Printf("%-45s %v\n", err, engine.Retryable(err))
	}
	// Output:
	// read: connection reset by peer                true
	// hash mismatch: sent 0001, destination has 0002 true
	// create: permission denied                     false
	// 553 Could not create file.                    false
}
