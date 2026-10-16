package mcpadapter

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/tesseract/internal/testutil/storetemplate"
)

var testStoreTemplate *storetemplate.Template

func TestMain(m *testing.M) {
	var err error
	testStoreTemplate, err = storetemplate.New(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "prepare MCP adapter test store:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
