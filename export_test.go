package wgipam

import "time"

// Functions in this file should only be exported for use in tests.

// TimeNow wraps timeNow.
func TimeNow() time.Time { return timeNow() }

// StrKey wraps strKey.
func StrKey(s string) uint64 { return strKey(s) }
