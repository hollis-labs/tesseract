//go:build (!darwin && !linux) || (darwin && !cgo)

package runtimeimage

import (
	"context"
	"fmt"
)

func runningDigest() (string, string, error) {
	return "", "", fmt.Errorf("running-image verification unsupported in this build")
}

func candidateDigest(context.Context, string) (string, string, error) {
	return "", "", fmt.Errorf("candidate verification unsupported in this build")
}
