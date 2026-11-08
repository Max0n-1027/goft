//go:build !windows

package cmd

// underServiceManager reports whether goft was started by the Windows service
// manager, which outside Windows it never is.
func underServiceManager() bool { return false }

// runUnderServiceManager is never called outside Windows.
func runUnderServiceManager() error { return nil }
