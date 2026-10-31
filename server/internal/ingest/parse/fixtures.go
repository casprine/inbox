package parse

import "embed"

// Fixtures are the sample messages used by `inboxd admin seed-demo` and tests.
//
//go:embed testdata/*.eml
var Fixtures embed.FS
