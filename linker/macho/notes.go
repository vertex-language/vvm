package macho

import (
	"crypto/sha256"
)

// contentUUID derives a deterministic (not random) UUID-ish 16 bytes from
// the bytes written so far, so re-linking identical inputs is reproducible —
// unlike a random UUID, which would make builds non-deterministic.
func contentUUID(soFar []byte) [16]byte {
	h := sha256.Sum256(soFar)
	var u [16]byte
	copy(u[:], h[:16])
	// Set version/variant bits so tools that validate UUID shape don't choke.
	u[6] = (u[6] & 0x0F) | 0x40 // version 4
	u[8] = (u[8] & 0x3F) | 0x80 // RFC 4122 variant
	return u
}

func appendUUID(buf []byte, soFarForHash []byte) []byte {
	buf = u32(buf, LC_UUID)
	buf = u32(buf, uint32(uuidCmdSize))
	u := contentUUID(soFarForHash)
	return append(buf, u[:]...)
}

// appendSourceVersion emits LC_SOURCE_VERSION (informational; 0 unless the
// caller threads a real VCS version through emitRequest).
func appendSourceVersion(buf []byte, encoded uint64) []byte {
	buf = u32(buf, LC_SOURCE_VERSION)
	buf = u32(buf, uint32(sourceVersionCmdSize))
	return u64(buf, encoded)
}