package skillpkg

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// mapSeparators normalizes a source path to POSIX form by replacing the Windows
// separator with "/". Only the separator is mapped; the path is then validated in
// its canonical form. No cleaning or rewriting is performed.
func mapSeparators(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// ValidatePath reports whether p is a canonical POSIX relative path. It is the
// single authoritative path rule set: Build and Decode apply it here, and the
// source-intake layer (internal/skillsource) calls it directly so the two layers
// can never develop a second, slightly different rule set (source-intake ADR D6).
func ValidatePath(p string) error { return validatePath(p) }

// validatePath reports whether p is a canonical POSIX relative path: valid UTF-8,
// non-empty, no leading or trailing "/", no backslash, no NUL, no Windows drive
// prefix, and no ".", "..", or empty component. It rejects rather than cleans.
func validatePath(p string) error {
	if !utf8.ValidString(p) {
		return fmt.Errorf("%w: not valid UTF-8", ErrPath)
	}
	if p == "" {
		return fmt.Errorf("%w: empty path", ErrPath)
	}
	if strings.IndexByte(p, 0) >= 0 {
		return fmt.Errorf("%w: NUL in %q", ErrPath, p)
	}
	if strings.IndexByte(p, '\\') >= 0 {
		return fmt.Errorf("%w: backslash separator in %q", ErrPath, p)
	}
	if p[0] == '/' {
		return fmt.Errorf("%w: absolute path %q", ErrPath, p)
	}
	if strings.HasSuffix(p, "/") {
		return fmt.Errorf("%w: trailing separator in %q", ErrPath, p)
	}
	if isDrivePrefix(p) {
		return fmt.Errorf("%w: drive prefix in %q", ErrPath, p)
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "":
			return fmt.Errorf("%w: empty component in %q", ErrPath, p)
		case ".":
			return fmt.Errorf("%w: dot component in %q", ErrPath, p)
		case "..":
			return fmt.Errorf("%w: parent (..) component in %q", ErrPath, p)
		}
	}
	return nil
}

// isDrivePrefix reports whether p begins with a Windows drive prefix ("C:").
func isDrivePrefix(p string) bool {
	if len(p) < 2 {
		return false
	}
	c := p[0]
	return p[1] == ':' && ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'))
}

// foldKey returns the case-insensitive collision key for a path (Unicode simple
// lowercase). It is used only for collision detection; the stored path is unchanged.
func foldKey(p string) string { return strings.ToLower(p) }
