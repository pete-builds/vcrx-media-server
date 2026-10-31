//go:build !vcrxdev

package main

import (
	"log/slog"
	"time"
)

// devHomeDate is nil in every normal build, the release image included: Home
// goes by the real date. Only a build with -tags vcrxdev (scripts/dev-server.sh)
// reads VCRX_DEV_HOME_DATE; see dev_tagged.go.
func devHomeDate(*slog.Logger) func() time.Time { return nil }

// devCommand runs development-only subcommands; there are none in a normal build.
func devCommand(string, []string) (bool, error) { return false, nil }
