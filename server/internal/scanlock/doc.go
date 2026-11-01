// Package scanlock holds the per-library lock files a scan keeps for its whole
// run, in the server's config folder. It lives outside the scanner packages,
// which must never create or change a file (TestNoWritesInSource), and it never
// touches media.
package scanlock
