package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/textproto"
	"testing"
)

func TestRetryableClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},

		// Permanent: the next attempt would fail the same way.
		{"cancelled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"missing file", fmt.Errorf("open: %w", fs.ErrNotExist), false},
		{"permission denied", fmt.Errorf("write: %w", fs.ErrPermission), false},
		{"already exists", fmt.Errorf("move: %w", fs.ErrExist), false},
		{"ftp permanent reply", &textproto.Error{Code: 553, Msg: "Could not create file."}, false},

		// Worth another go.
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"timeout", &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, true},
		{"ftp transient reply", &textproto.Error{Code: 450, Msg: "file busy"}, true},
		// A digest that did not match can be corruption in flight, and a second
		// attempt is exactly how that gets fixed.
		{"hash mismatch", errors.New("hash mismatch: sent 0001, destination has 0002"), true},
		{"anything unrecognised", errors.New("something odd happened"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Retryable(tc.err); got != tc.want {
				t.Errorf("Retryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryableLooksThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("transfer a.csv: %w", fmt.Errorf("stat: %w", fs.ErrNotExist))
	if Retryable(wrapped) {
		t.Error("a wrapped permanent error is still permanent")
	}
}
