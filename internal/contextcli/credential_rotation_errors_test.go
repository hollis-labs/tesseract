package contextcli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestCredentialDiagnosticsNeverExposeStoreErrorValues(t *testing.T) {
	for _, err := range []error{errors.New("synthetic-sensitive-value"), fmt.Errorf("synthetic-sensitive-value: %w", contextstore.ErrCredentialConflict), fmt.Errorf("synthetic-sensitive-value: %w", contextstore.ErrAuthTokenRevoked)} {
		message := credentialCLIMessage(err)
		if strings.Contains(message, "synthetic-sensitive-value") || message == "" {
			t.Fatal("credential diagnostic exposed underlying error values")
		}
	}
}
