package memory

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/testutil/storetemplate"
)

var testStoreTemplate *storetemplate.Template

func TestMain(m *testing.M) {
	var err error
	testStoreTemplate, err = storetemplate.New(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "prepare memory test store:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// OpenTestContextStore is available to both memory and memory_test fixtures.
// Each call copies the migrated template into the test's own temporary root.
func OpenTestContextStore(ctx context.Context, cfg contextstore.Config) (*contextstore.Store, error) {
	return testStoreTemplate.Open(ctx, cfg)
}
