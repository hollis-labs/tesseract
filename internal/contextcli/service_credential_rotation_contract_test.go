package contextcli

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestServiceCredentialLegacyRawRotateRefusesBeforeEffects(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	ctx := context.Background()
	raw, _, err := cli.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "tether-proxy", Scopes: []string{"write"}, NamespaceGlobs: []string{"project/example/*"}})
	if err != nil {
		t.Fatal("fixture issuance failed")
	}
	before, err := cli.Store.ListAuthTokens(ctx, 100)
	if err != nil {
		t.Fatal("fixture metadata read failed")
	}
	code := cli.Run(ctx, []string{"context", "token", "rotate", "--token", raw})
	if code == 0 {
		t.Error("legacy raw-token rotation was accepted")
	}
	if strings.Contains(out.String(), raw) || strings.Contains(errOut.String(), raw) {
		t.Error("legacy refusal disclosed the supplied credential")
	}
	after, err := cli.Store.ListAuthTokens(ctx, 100)
	if err != nil {
		t.Fatal("result metadata read failed")
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("legacy refusal changed credential metadata")
	}
	if err := cli.Store.ValidateAuthToken(ctx, raw); err != nil {
		t.Error("legacy refusal invalidated the existing service credential")
	}
}
