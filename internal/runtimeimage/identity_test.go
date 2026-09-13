//go:build linux || (darwin && cgo)

package runtimeimage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/go-mcp/staleness"
)

func TestRunningImageSurvivesReplacementAndRejectsLateAcquisition(t *testing.T) {
	dir := t.TempDir()
	build := func(label string) string {
		t.Helper()
		path := filepath.Join(dir, "probe-"+label)
		cmd := exec.Command("go", "build", "-ldflags", "-X main.label="+label, "-o", path, "./testdata/probe") // #nosec G204 -- Fixed local probe source, constant labels, and a t.TempDir output.
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		return path
	}
	a, b := build("A"), build("B")
	copyImage := func(source, target string) {
		t.Helper()
		data, err := os.ReadFile(source) // #nosec G304 -- Source is one of the probe images built above inside t.TempDir.
		if err != nil {
			t.Fatal(err)
		}
		tmp := target + ".next"
		if err := os.WriteFile(tmp, data, 0700); err != nil { // #nosec G306 G703 -- Source and target are fixed probe paths inside t.TempDir; real replacement requires executable bytes.
			t.Fatal(err)
		}
		if err := os.Rename(tmp, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, late := range []bool{false, true} {
		name := "captured-before-replacement"
		if late {
			name = "replaced-before-first-capture"
		}
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(dir, name)
			copyImage(a, target)
			cmd := exec.Command(target, target) // #nosec G204 -- Run only the probe just built and copied into t.TempDir.
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if startErr := cmd.Start(); startErr != nil {
				t.Fatal(startErr)
			}
			t.Cleanup(func() {
				_ = stdin.Close()
				if waitErr := cmd.Wait(); waitErr != nil {
					t.Errorf("probe cleanup: %v %s", waitErr, stderr.String())
				}
			})
			reader := bufio.NewReader(stdout)
			ready, err := reader.ReadString('\n')
			if err != nil || !strings.HasPrefix(ready, "ready A") {
				t.Fatalf("probe: %q %v", ready, err)
			}
			send := func(command string, result any) {
				t.Helper()
				if _, err := io.WriteString(stdin, command+"\n"); err != nil {
					t.Fatal(err)
				}
				line, err := reader.ReadBytes('\n')
				if err != nil {
					t.Fatalf("probe read: %v %s", err, stderr.String())
				}
				if err := json.Unmarshal(line, result); err != nil {
					t.Fatalf("probe JSON: %v %q", err, line)
				}
			}
			var captured struct {
				Identity staleness.Identity `json:"identity"`
				Error    string             `json:"error"`
			}
			if !late {
				send("capture", &captured)
				if captured.Error != "" || captured.Identity.Digest == "" {
					t.Fatalf("running verification failed: %+v", captured)
				}
				var same staleness.Observation
				send("observe", &same)
				if same.State != staleness.Same {
					t.Fatalf("initial candidate: %+v", same)
				}
				copyImage(a, target)
				send("observe", &same)
				if same.State != staleness.Same {
					t.Fatalf("identical bytes: %+v", same)
				}
			}
			copyImage(b, target)
			if late {
				send("capture", &captured)
				if runtime.GOOS == "darwin" && (captured.Error == "" || captured.Identity.Digest != "") {
					t.Fatalf("claimed replacement as running identity: %+v", captured)
				}
			}
			var observed staleness.Observation
			send("observe", &observed)
			want := staleness.Different
			if late && runtime.GOOS == "darwin" {
				want = staleness.Unknown
			}
			if observed.State != want {
				t.Fatalf("replacement: got %+v, want %s", observed, want)
			}
		})
	}
}
