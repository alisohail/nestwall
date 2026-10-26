// Package buildinfo holds the version stamped into the binary at build time.
package buildinfo

// Version is set at build time with -ldflags "-X nestwall/backend/internal/buildinfo.Version=<tag>".
// A binary built without the flag reports "dev".
var Version = "dev"
