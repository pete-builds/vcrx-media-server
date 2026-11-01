//go:build !unix

package scanlock

// Library is a no-op where flock is not available: scans are then guarded by
// their run rows alone, and server start closes every open run.
func Library(string, int64) (func(), bool, error) { return func() {}, false, nil }
