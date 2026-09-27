package skillstore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wanglongan587/cloud/internal/skillpkg"
)

// locatorPrefix is the fixed leading segment of every logical object key.
const locatorPrefix = "skills"

// isKnownFormat reports whether name is a package format this package can
// address. Today that is exactly the canonical container family; a future format
// must be added here and to the decoder deliberately.
func isKnownFormat(name string) bool { return name == skillpkg.FormatName }

// isKnownAlgorithm reports whether name is a digest algorithm this package can
// address. Today that is exactly SHA-256.
func isKnownAlgorithm(name string) bool { return name == AlgorithmSHA256 }

// Locator is the provider-independent logical object key — the durable
// object_locator of ADR D17. It is derived deterministically from
// (package_format, package_format_version, digest_algorithm, package_digest) and
// carries no business identity. A Locator is always produced by NewLocator or
// ParseLocator, never by concatenating strings.
type Locator struct {
	format    string
	version   int
	algorithm string
	digestHex string
}

// NewLocator constructs a validated logical object locator from the four
// content-derived identity components. It rejects unknown formats, unknown digest
// algorithms, non-positive versions, and non-canonical digest encodings.
func NewLocator(format string, version int, algorithm, digestHex string) (Locator, error) {
	if !isKnownFormat(format) {
		return Locator{}, fmt.Errorf("%w: unknown package format %q", ErrLocator, format)
	}
	if version < 1 {
		return Locator{}, fmt.Errorf("%w: non-positive package format version %d", ErrLocator, version)
	}
	if !isKnownAlgorithm(algorithm) {
		return Locator{}, fmt.Errorf("%w: unknown digest algorithm %q", ErrLocator, algorithm)
	}
	if !isLowerHex64(digestHex) {
		return Locator{}, fmt.Errorf("%w: package digest must be 64 lowercase hex chars, got %q", ErrLocator, digestHex)
	}
	return Locator{format: format, version: version, algorithm: algorithm, digestHex: digestHex}, nil
}

// ParseLocator parses a durable object_locator string back into a Locator. It
// validates the canonical structure and component allowlists but never re-derives
// the digest from content; the input string is the authoritative key (ADR D11).
func ParseLocator(s string) (Locator, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 5 {
		return Locator{}, fmt.Errorf("%w: want %q/<format>/v<version>/<algorithm>/<digest>, got %q", ErrLocator, locatorPrefix, s)
	}
	if parts[0] != locatorPrefix {
		return Locator{}, fmt.Errorf("%w: prefix must be %q", ErrLocator, locatorPrefix)
	}
	format, verPart, algorithm, digestHex := parts[1], parts[2], parts[3], parts[4]
	if !isKnownFormat(format) {
		return Locator{}, fmt.Errorf("%w: unknown package format %q", ErrLocator, format)
	}
	if len(verPart) < 2 || verPart[0] != 'v' {
		return Locator{}, fmt.Errorf("%w: version segment %q", ErrLocator, verPart)
	}
	version, err := strconv.Atoi(verPart[1:])
	if err != nil || version < 1 {
		return Locator{}, fmt.Errorf("%w: version segment %q", ErrLocator, verPart)
	}
	if !isKnownAlgorithm(algorithm) {
		return Locator{}, fmt.Errorf("%w: unknown digest algorithm %q", ErrLocator, algorithm)
	}
	if !isLowerHex64(digestHex) {
		return Locator{}, fmt.Errorf("%w: package digest must be 64 lowercase hex chars", ErrLocator)
	}
	return Locator{format: format, version: version, algorithm: algorithm, digestHex: digestHex}, nil
}

// String returns the canonical provider-independent key
// "skills/<format>/v<version>/<algorithm>/<digest>". It is the exact value an
// adapter uses to address the object and the value persisted as object_locator.
func (l Locator) String() string {
	return locatorPrefix + "/" + l.format + "/v" + strconv.Itoa(l.version) + "/" + l.algorithm + "/" + l.digestHex
}

// Format returns the package format name (e.g. "ora-skill-package").
func (l Locator) Format() string { return l.format }

// Version returns the package format version.
func (l Locator) Version() int { return l.version }

// Algorithm returns the digest algorithm (e.g. "sha256").
func (l Locator) Algorithm() string { return l.algorithm }

// DigestHex returns the package digest embedded in the locator (lowercase 64-hex).
func (l Locator) DigestHex() string { return l.digestHex }

// isLowerHex64 reports whether s is exactly 64 lowercase hexadecimal characters.
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
