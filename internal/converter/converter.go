package converter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
)

const maxGPXBytes = 10 * 1024 * 1024 // 10 MB cap on subprocess stdout

// Converter runs the alltrailsgpx binary as a subprocess.
type Converter struct {
	binPath string
	log     *slog.Logger
}

// New returns a Converter that uses the given binary path.
// It verifies the binary is executable at construction time.
func New(binPath string, log *slog.Logger) (*Converter, error) {
	if _, err := exec.LookPath(binPath); err != nil {
		return nil, fmt.Errorf("alltrailsgpx binary not found at %q: %w", binPath, err)
	}
	return &Converter{binPath: binPath, log: log}, nil
}

// Convert pipes trailJSON into alltrailsgpx via stdin and returns the GPX
// bytes from stdout. The context controls the subprocess lifetime.
func (c *Converter) Convert(ctx context.Context, trailJSON []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binPath)
	cmd.Stdin = bytes.NewReader(trailJSON)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("converter: failed to open stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("converter: failed to start process: %w", err)
	}

	gpx, readErr := io.ReadAll(io.LimitReader(stdout, maxGPXBytes))

	if err := cmd.Wait(); err != nil {
		c.log.Error("alltrailsgpx exited with error", "stderr", stderr.String(), "err", err)
		return nil, fmt.Errorf("converter: process exited with error: %w", err)
	}

	if stderr.Len() > 0 {
		c.log.Debug("alltrailsgpx stderr", "output", stderr.String())
	}

	if readErr != nil {
		return nil, fmt.Errorf("converter: failed to read stdout: %w", readErr)
	}

	return gpx, nil
}
