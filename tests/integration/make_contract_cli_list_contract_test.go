package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeContractListGolden struct {
	Markers []string `json:"markers"`
}

func TestMakeContractCLIListContractAgainstGolden(t *testing.T) {
	golden := loadMakeContractListGolden(t)
	cmd := exec.Command("make", "contract-cli-list")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make contract-cli-list failed: %v\noutput:\n%s", err, string(out))
	}
	content := string(out)
	pos := 0
	for _, marker := range golden.Markers {
		next := strings.Index(content[pos:], marker)
		if next < 0 {
			t.Fatalf("missing marker %q in output:\n%s", marker, content)
		}
		pos += next + len(marker)
	}
}

func loadMakeContractListGolden(t *testing.T) makeContractListGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_contract_cli_list_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make contract-cli-list golden: %v", err)
	}
	var out makeContractListGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make contract-cli-list golden: %v", err)
	}
	return out
}
