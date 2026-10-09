package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/plugin-mcp/go-mcp/staleness"
	"github.com/hollis-labs/tesseract/internal/runtimeimage"
)

var label string

func main() {
	fmt.Println("ready", label)
	var running staleness.Identity
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "capture":
			identity, err := runtimeimage.Running()
			running = identity
			message := ""
			if err != nil {
				message = err.Error()
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"identity": identity, "error": message})
		case "observe":
			_ = json.NewEncoder(os.Stdout).Encode(staleness.Observe(context.Background(), running, staleness.Target{Kind: "absolute", Selector: os.Args[1]}, runtimeimage.Inspect))
		}
	}
}
