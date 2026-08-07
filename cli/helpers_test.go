package cli

import (
	"io"
	"os"
	"testing"
)

// captureStdout swaps os.Stdout for a pipe. Commands write parseable data
// there, so tests have to read it rather than the returned value.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()

	_ = w.Close()
	os.Stdout = orig
	return <-done
}
