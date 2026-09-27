package skillpkg

// maxFileCountCeiling caps MaxFileCount at MaxInt32, so the uint32 file_count
// field serialized by encodeManifest can never overflow. MaxInt32 is far above
// any plausible Skill and fits the uint32 wire field on every platform.
const maxFileCountCeiling = int(^uint32(0) >> 1)

// Limits bounds canonicalization and decoding. Every byte-size limit is a uint64;
// a zero value for any field means "use the default" (see DefaultLimits). Byte-size
// fields are checked before any matching allocation, and every length-prefixed
// field is checked against both the configured limit and the remaining input
// before it is indexed.
type Limits struct {
	// MaxFileCount caps the number of canonical files in one Skill.
	MaxFileCount int
	// MaxPathBytes caps a canonical path's total UTF-8 byte length.
	MaxPathBytes uint64
	// MaxPathComponentBytes caps a single "/"-separated component.
	MaxPathComponentBytes uint64
	// MaxSingleFileBytes caps any one file's byte length.
	MaxSingleFileBytes uint64
	// MaxTotalBytes caps the sum of all file byte lengths.
	MaxTotalBytes uint64
	// MaxManifestBytes caps the canonical ManifestV1 length.
	MaxManifestBytes uint64
	// MaxPackageBytes caps the whole ora-skill-package v1 object length.
	MaxPackageBytes uint64
	// MaxSkillMDBytes caps the SKILL.md file length.
	MaxSkillMDBytes uint64
}

// DefaultLimits returns the library's conservative default bounds. These are
// provisional library defaults, not a product quota and not a database schema
// (quota configuration is a later step); a product config must not silently widen
// them.
func DefaultLimits() Limits {
	return Limits{
		MaxFileCount:          4096,
		MaxPathBytes:          1024,
		MaxPathComponentBytes: 255,
		MaxSingleFileBytes:    256 << 20, // 256 MiB
		MaxTotalBytes:         1 << 30,   // 1 GiB
		MaxManifestBytes:      8 << 20,   // 8 MiB
		MaxPackageBytes:       (1 << 30) + (16 << 20),
		MaxSkillMDBytes:       1 << 20, // 1 MiB
	}
}

// normalized fills zero (unset) fields from DefaultLimits so a zero-value Limits
// behaves as the default configuration.
func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxFileCount <= 0 {
		l.MaxFileCount = d.MaxFileCount
	}
	if l.MaxFileCount > maxFileCountCeiling {
		l.MaxFileCount = maxFileCountCeiling
	}
	if l.MaxPathBytes == 0 {
		l.MaxPathBytes = d.MaxPathBytes
	}
	if l.MaxPathComponentBytes == 0 {
		l.MaxPathComponentBytes = d.MaxPathComponentBytes
	}
	if l.MaxSingleFileBytes == 0 {
		l.MaxSingleFileBytes = d.MaxSingleFileBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxManifestBytes == 0 {
		l.MaxManifestBytes = d.MaxManifestBytes
	}
	if l.MaxPackageBytes == 0 {
		l.MaxPackageBytes = d.MaxPackageBytes
	}
	if l.MaxSkillMDBytes == 0 {
		l.MaxSkillMDBytes = d.MaxSkillMDBytes
	}
	return l
}
