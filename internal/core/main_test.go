package core

import (
	"os"
	"testing"
)

// The tests check media details and tags right after indexing; in use they
// arrive moments later, in the background (TestFilesAreSearchableBeforeTheirHeadersAreRead).
func TestMain(m *testing.M) {
	syncBackground = true
	os.Exit(m.Run())
}
