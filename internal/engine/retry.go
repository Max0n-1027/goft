package engine

import (
	"context"
	"errors"
	"io/fs"
	"net/textproto"
)

// Retryable reports whether another attempt at err could plausibly succeed.
//
// The classification is by exclusion: a small set of errors is known to be
// permanent, and everything else is worth one more go. Enumerating every
// transient error a protocol library might produce is not possible, and being
// too strict would leave exactly the failures retrying was meant to survive
// sitting unretried.
//
// Permanent means the next attempt would fail in the same way:
//
//   - the run is shutting down,
//   - the file or directory is not there,
//   - the credentials do not allow it,
//   - the destination already holds a file the action refuses to replace,
//   - the server gave a 5xx reply, which FTP defines as a permanent negative.
//
// Verification failures are deliberately absent: a hash mismatch can come from
// corruption in flight, which is precisely what a second attempt fixes.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, fs.ErrNotExist):
		return false
	case errors.Is(err, fs.ErrPermission):
		return false
	case errors.Is(err, fs.ErrExist):
		return false
	}

	// FTP replies carry the answer in their first digit: 4xx is a transient
	// negative and invites a retry, 5xx is permanent.
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 500 {
		return false
	}

	return true
}
