package cache

// Layout documents the on-disk shape of the cache. The structure is owned
// by this package; nothing else in core/bundle/ may construct paths into
// the cache directly — they must go through the CAS interface.
//
//	<data_dir>/
//	  cache/
//	    sha256/<aa>/<bb>/<digest-hex>   # canonical artifact bytes
//	    staging/<random-hex>             # in-flight downloads (deleted on cancel/mismatch)
//	    manifests/sha256/<aa>/<bb>/<...> # parsed-and-validated manifest cache
//
// Two-hex-char nesting (aa/bb) bounds any one directory's child count to
// ~256, which keeps directory enumeration cheap on every filesystem we
// target (ext4, APFS, NTFS) without going so deep that we trigger
// path-length limits on Windows.
//
// SHA-256 digests are case-stable hex; canonical lookup never has to
// normalize case. We never write digests in upper case so a case-
// insensitive filesystem (HFS+, NTFS-default) cannot collide two distinct
// digests on the same path.

// (Layout doc only — concrete path math lives in cas.go.)
