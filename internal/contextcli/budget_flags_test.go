package contextcli

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestCLIBudgetDefaultsMatchPacketAssemblyContract(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	if code := cli.Run(context.Background(), []string{"context", "broker", "plan", "--output", "json"}); code != 0 {
		t.Fatalf("broker plan failed: %s", errOut.String())
	}

	var body struct {
		Plan struct {
			Assembly struct {
				Budget struct {
					MaxItems          int `json:"max_items"`
					MaxTokensEstimate int `json:"max_tokens_estimate"`
				} `json:"budget"`
			} `json:"assembly"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode broker plan: %v", err)
	}
	if body.Plan.Assembly.Budget.MaxItems != 50 || body.Plan.Assembly.Budget.MaxTokensEstimate != 8000 {
		t.Fatalf("default budget = %+v, want max_items=50 max_tokens_estimate=8000", body.Plan.Assembly.Budget)
	}
	if errOut.Len() != 0 {
		t.Fatalf("canonical defaults emitted a warning: %s", errOut.String())
	}
}

func TestCLIBudgetCanonicalNamesDrivePlanAndGeneratedHint(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	if code := cli.Run(context.Background(), []string{
		"context", "broker", "plan",
		"--max-items", "7", "--max-tokens-estimate", "1234", "--output", "json",
	}); code != 0 {
		t.Fatalf("broker plan failed: %s", errOut.String())
	}

	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode broker plan: %v", err)
	}
	budget := body["plan"].(map[string]any)["assembly"].(map[string]any)["budget"].(map[string]any)
	if budget["max_items"] != float64(7) || budget["max_tokens_estimate"] != float64(1234) {
		t.Fatalf("budget = %v", budget)
	}
	if errOut.Len() != 0 {
		t.Fatalf("canonical flags emitted a warning: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "broker", "plan", "--max-items", "7", "--max-tokens-estimate", "1234",
	}); code != 0 {
		t.Fatalf("human broker plan failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "--max-items 7 --max-tokens-estimate 1234") {
		t.Fatalf("generated command does not use canonical flags:\n%s", out.String())
	}
	if strings.Contains(out.String(), "--budget-items") || strings.Contains(out.String(), "--budget-tokens") {
		t.Fatalf("generated command contains deprecated flags:\n%s", out.String())
	}
}

func TestCLIBrokerBootProjectEnforcesItemFloor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested int
		want      int
	}{
		{name: "raises small request", requested: 7, want: 100},
		{name: "preserves larger request", requested: 125, want: 125},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, out, errOut := newTestCLI(t)
			if code := cli.Run(context.Background(), []string{
				"context", "broker", "plan", "--intent", "boot_project",
				"--max-items", strconv.Itoa(tc.requested), "--max-tokens-estimate", "1234", "--output", "json",
			}); code != 0 {
				t.Fatalf("broker plan failed: %s", errOut.String())
			}

			var body map[string]any
			if err := json.Unmarshal(out.Bytes(), &body); err != nil {
				t.Fatalf("decode broker plan: %v", err)
			}
			budget := body["plan"].(map[string]any)["assembly"].(map[string]any)["budget"].(map[string]any)
			if budget["max_items"] != float64(tc.want) || budget["max_tokens_estimate"] != float64(1234) {
				t.Fatalf("budget = %v, want max_items=%d max_tokens_estimate=1234", budget, tc.want)
			}
		})
	}
}

func TestCLIBudgetAliasesApplyAndWarn(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	if code := cli.Run(context.Background(), []string{
		"context", "broker", "plan",
		"--budget-items", "9", "--budget-tokens", "2345", "--output", "json",
	}); code != 0 {
		t.Fatalf("broker plan alias failed: %s", errOut.String())
	}

	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode broker plan: %v", err)
	}
	budget := body["plan"].(map[string]any)["assembly"].(map[string]any)["budget"].(map[string]any)
	if budget["max_items"] != float64(9) || budget["max_tokens_estimate"] != float64(2345) {
		t.Fatalf("deprecated aliases did not reach the canonical budget: %v", budget)
	}
	for _, want := range []string{
		"--budget-items is deprecated", "use --max-items",
		"--budget-tokens is deprecated", "use --max-tokens-estimate",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("warning %q missing from stderr: %s", want, errOut.String())
		}
	}
}

func TestCLIBudgetAliasesRejectCanonicalConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"packet/items", []string{"context", "packet", "--max-items", "1", "--budget-items", "2"}, "--max-items and deprecated --budget-items"},
		{"packet/tokens", []string{"context", "packet", "--max-tokens-estimate", "1", "--budget-tokens", "2"}, "--max-tokens-estimate and deprecated --budget-tokens"},
		{"broker-plan/items", []string{"context", "broker", "plan", "--max-items", "1", "--budget-items", "2"}, "--max-items and deprecated --budget-items"},
		{"broker-fetch/tokens", []string{"context", "broker", "fetch", "--max-tokens-estimate", "1", "--budget-tokens", "2"}, "--max-tokens-estimate and deprecated --budget-tokens"},
		{"context-pack/items", []string{"context", "context-pack", "--max-items", "1", "--limit", "2"}, "--max-items and deprecated --limit"},
		{"context-pack/tokens", []string{"context", "context-pack", "--max-tokens-estimate", "1", "--max-tokens", "2"}, "--max-tokens-estimate and deprecated --max-tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, out, errOut := newTestCLI(t)
			if code := cli.Run(context.Background(), tc.args); code == 0 {
				t.Fatalf("conflicting flags exited 0; stdout: %s", out.String())
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tc.want)
			}
		})
	}
}

func TestCLIPacketCanonicalAndAliasBudgets(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	seedPacketRecord(t, cli, "user/memory/cli-budget", "one", `{"n":1}`)
	seedPacketRecord(t, cli, "user/memory/cli-budget", "two", `{"n":2}`)

	for _, tc := range []struct {
		name        string
		budgetFlags []string
		warning     string
	}{
		{"canonical", []string{"--max-items", "1", "--max-tokens-estimate", "8000"}, ""},
		{"deprecated aliases", []string{"--budget-items", "1", "--budget-tokens", "8000"}, "--budget-items is deprecated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			args := []string{"context", "packet", "--namespace", "user/memory/cli-budget", "--no-pins", "--output", "json"}
			args = append(args, tc.budgetFlags...)
			if code := cli.Run(context.Background(), args); code != 0 {
				t.Fatalf("packet failed: %s", errOut.String())
			}
			var body map[string]any
			if err := json.Unmarshal(out.Bytes(), &body); err != nil {
				t.Fatalf("decode packet: %v", err)
			}
			if got := len(body["items"].([]any)); got != 1 {
				t.Fatalf("items = %d, want 1", got)
			}
			if warning := errOut.String(); tc.warning == "" && warning != "" {
				t.Fatalf("canonical flags emitted a warning: %s", warning)
			} else if tc.warning != "" && !strings.Contains(warning, tc.warning) {
				t.Fatalf("warning = %q, want %q", warning, tc.warning)
			}
		})
	}
}

func TestCLIBudgetHelpUsesCanonicalNamesOnEveryAssemblyCommand(t *testing.T) {
	for _, verbs := range [][]string{
		{"packet", "--help"},
		{"broker", "plan", "--help"},
		{"broker", "fetch", "--help"},
		{"context-pack", "--help"},
	} {
		t.Run(strings.Join(verbs, " "), func(t *testing.T) {
			stdout := &strings.Builder{}
			stderr := &strings.Builder{}
			code, handled := Help(context.Background(), stdout, stderr, verbs)
			if !handled || code != 0 {
				t.Fatalf("handled=%v code=%d stderr=%s", handled, code, stderr.String())
			}
			for _, want := range []string{"-max-items", "-max-tokens-estimate"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help missing %s:\n%s", want, stdout.String())
				}
			}
		})
	}
}
