package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func captureProcessOutput(t *testing.T, stream **os.File, fn func()) (output string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *stream
	*stream = writer
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(reader)
		done <- readResult{data, err}
	}()
	defer func() {
		*stream = old
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
		result := <-done
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
		if result.err != nil {
			t.Error(result.err)
		}
		output = string(result.data)
	}()
	fn()
	return output
}

func TestCaptureCLIOutputBeyondPipeCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCaptureCLIOutputHelper$")
	cmd.Env = append(os.Environ(), "REASONIX_CAPTURE_HELPER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("large output capture did not complete: %v (context=%v)\n%s", err, ctx.Err(), out)
	}
}

func TestCaptureCLIOutputHelper(t *testing.T) {
	if os.Getenv("REASONIX_CAPTURE_HELPER") != "1" {
		return
	}
	payload := strings.Repeat("output", 1<<20)
	out, stderr := captureCLIOutput(t, func() {
		fmt.Fprint(os.Stdout, payload)
		fmt.Fprint(os.Stderr, payload)
	})
	if out != payload || stderr != payload {
		t.Fatal("capture truncated or mixed the output streams")
	}
}

func TestCaptureCLIOutputRestoresStreamsAfterPanic(t *testing.T) {
	out, stderr := os.Stdout, os.Stderr
	func() {
		defer func() {
			if recover() != "capture fixture" {
				t.Error("capture swallowed the panic")
			}
		}()
		captureCLIOutput(t, func() { panic("capture fixture") })
	}()
	if os.Stdout != out || os.Stderr != stderr {
		t.Fatal("capture retained process-global streams after panic")
	}
}
