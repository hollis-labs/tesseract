//go:build linux

package runtimeimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func runningDigest() (string, string, error) {
	// /proc/self/exe resolves the executing image, including after its original
	// pathname is unlinked or replaced. Linux denies writes to executing files.
	return linuxDigest(context.Background(), "/proc/self/exe")
}

func candidateDigest(ctx context.Context, path string) (string, string, error) {
	return linuxDigest(ctx, path)
}

func linuxDigest(ctx context.Context, path string) (string, string, error) {
	f, err := os.Open(path) // #nosec G304 -- Only /proc/self/exe or the private snapshot from staleness.Observe reaches this internal helper.
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 256<<20 {
		return "", "", fmt.Errorf("image unavailable or too large")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	reader := io.LimitReader(f, (256<<20)+1)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		n, readErr := reader.Read(buf)
		total += int64(n)
		_, _ = h.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", readErr
		}
	}
	after, err := f.Stat()
	if err != nil || total != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", "", fmt.Errorf("image changed during verification")
	}
	return "sha256", hex.EncodeToString(h.Sum(nil)), nil
}
