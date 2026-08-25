package fsys

import (
	"fmt"
	"io/fs"
	"strings"
)

// windowsDeviceNames are the names Windows reserves for devices. The name is
// reserved in every directory, and with any extension after it, so "aux" and
// "aux.csv" are equally unusable.
var windowsDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// windowsForbiddenRunes cannot appear in a Windows file name at all. Refusing
// them here only improves the message: the platform rejects them anyway, with
// "The filename, directory name, or volume label syntax is incorrect".
const windowsForbiddenRunes = `*?"<>|`

// checkStorableName reports why goos could not hold a file under exactly the
// name given, or nil when it can. name is a slash separated path relative to
// the root, as every [FS] name is, and each element is judged on its own.
//
// The names worth refusing are the ones Windows accepts and then quietly turns
// into something else:
//
//   - A colon opens an NTFS alternate data stream, so a file the server calls
//     "2026:01.csv" is written into a hidden stream of an empty file called
//     "2026". Nothing that lists the directory — Explorer, dir, a backup, the
//     next goft cycle — can see it again.
//   - A trailing dot or space is dropped, so "report.csv." arrives as
//     "report.csv", and two files that differ only there collide.
//   - A reserved device name may resolve to the device rather than to a file.
//
// Each of these survives goft's own verification, because reading the file
// back applies the same reinterpretation that writing it did, and the digests
// therefore agree. A transfer that cannot be caught afterwards has to be
// refused before anything is written, which is what this is for.
//
// goos is a parameter rather than [runtime.GOOS] so that the Windows rules can
// be exercised from any platform.
//
// The error wraps [fs.ErrInvalid], so that the engine treats it as permanent:
// the name would be refused identically on every further attempt.
func checkStorableName(name, goos string) error {
	if goos != "windows" {
		return nil
	}
	for _, elem := range strings.Split(name, "/") {
		if why := windowsNameProblem(elem); why != "" {
			return fmt.Errorf("%q cannot be stored on windows: %s: %w", name, why, fs.ErrInvalid)
		}
	}
	return nil
}

// windowsNameProblem describes what is wrong with one path element, or returns
// an empty string when nothing is.
func windowsNameProblem(elem string) string {
	// An empty element comes from a leading, trailing or doubled separator and
	// says nothing about the name itself; "." and ".." are the directory's own
	// entries rather than something being created.
	if elem == "" || elem == "." || elem == ".." {
		return ""
	}

	for _, r := range elem {
		switch {
		case r == ':':
			return `":" would open an NTFS alternate data stream rather than name a file`
		case r == '\\':
			return `"\" separates directories here rather than being part of a name`
		case strings.ContainsRune(windowsForbiddenRunes, r):
			return fmt.Sprintf("%q is not allowed in a file name", r)
		case r < 0x20:
			return fmt.Sprintf("the control character %U is not allowed in a file name", r)
		}
	}

	switch elem[len(elem)-1] {
	case '.':
		return "a trailing dot is dropped, so the file would be stored under a different name"
	case ' ':
		return "a trailing space is dropped, so the file would be stored under a different name"
	}

	if base, _, _ := strings.Cut(elem, "."); windowsDeviceNames[strings.ToUpper(base)] {
		return fmt.Sprintf("%q is a reserved device name, whatever extension follows it", base)
	}
	return ""
}
