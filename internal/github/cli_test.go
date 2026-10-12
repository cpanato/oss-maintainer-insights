package github

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLITransportUsesNativeCredentialsAndPreservesHTTPStatuses(t *testing.T) {
	directory := t.TempDir()
	program := "#!/bin/sh\nprintf 'HTTP/1.1 429 Too Many Requests\\r\\nContent-Length: 2\\r\\nX-RateLimit-Remaining: 0\\r\\nRetry-After: 60\\r\\n\\r\\n{}'\nexit 1\n"
	// #nosec G306 -- Owner-only executable fixture simulates gh without network or credentials.
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/example/repo", nil)
	response, err := (CLITransport{}).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "{}" || response.StatusCode != 429 || response.Header.Get("Retry-After") != "60" || response.Header.Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("lost HTTP failure metadata: %+v body=%s err=%v", response, body, err)
	}
	for _, url := range []string{"https://example.com/repos/example/repo", "http://api.github.com/repos/example/repo"} {
		request, _ = http.NewRequest(http.MethodGet, url, nil)
		if _, err = (CLITransport{}).RoundTrip(request); err == nil {
			t.Fatal("accepted foreign endpoint")
		}
	}
}
func TestCLIOutputBound(t *testing.T) {
	output := &limitedOutput{}
	if _, err := output.Write([]byte(strings.Repeat("x", maxBodyBytes+64*1024+1))); err == nil || !output.exceeded {
		t.Fatal("unbounded subprocess output")
	}
}
