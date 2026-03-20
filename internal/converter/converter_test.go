package converter

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew_BinaryNotFound(t *testing.T) {
	_, err := New("/nonexistent/binary/alltrailsgpx", discardLogger())
	if err == nil {
		t.Error("New() expected error for missing binary, got nil")
	}
}

// TestConvert_Cat uses /bin/cat as a stand-in binary: it reads stdin and writes
// it unchanged to stdout, which lets us test the piping plumbing without
// needing the real alltrailsgpx binary.
func TestConvert_Cat(t *testing.T) {
	c := &Converter{binPath: "/bin/cat", log: discardLogger()}
	input := []byte(`{"trails":[{"id":1}]}`)

	out, err := c.Convert(context.Background(), input)
	if err != nil {
		t.Fatalf("Convert() unexpected error: %v", err)
	}
	if string(out) != string(input) {
		t.Errorf("Convert() = %q, want %q", out, input)
	}
}

// TestConvert_Timeout verifies the subprocess is killed when the context
// deadline is exceeded. Uses /bin/sleep as the stand-in binary.
func TestConvert_Timeout(t *testing.T) {
	c := &Converter{binPath: "/bin/sleep", log: discardLogger()}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// Pass "10" as stdin — sleep will ignore it, but it satisfies the pipe.
	_, err := c.Convert(ctx, []byte("10"))
	if err == nil {
		t.Fatal("Convert() expected error from timeout, got nil")
	}
	if !strings.Contains(err.Error(), "converter: process exited") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestConvert_NonZeroExit verifies that a non-zero exit code is reported as an
// error. Uses /bin/false which always exits 1.
func TestConvert_NonZeroExit(t *testing.T) {
	c := &Converter{binPath: "/bin/false", log: discardLogger()}

	_, err := c.Convert(context.Background(), []byte("{}"))
	if err == nil {
		t.Fatal("Convert() expected error for non-zero exit, got nil")
	}
}

// TestConvert_RealBinary runs the full conversion against the real alltrailsgpx
// binary. Skipped when the binary is not on $PATH.
func TestConvert_RealBinary(t *testing.T) {
	c, err := New("alltrailsgpx", discardLogger())
	if err != nil {
		t.Skipf("alltrailsgpx not found, skipping: %v", err)
	}

	// Minimal valid AllTrails API JSON that alltrailsgpx can convert.
	// Replace with a real fixture if available.
	fixtureJSON := []byte(`{"trails":[]}`)

	out, err := c.Convert(context.Background(), fixtureJSON)
	if err != nil {
		t.Fatalf("Convert() unexpected error: %v", err)
	}
	if len(out) == 0 {
		t.Error("Convert() returned empty output")
	}
}
