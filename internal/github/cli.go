package github

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os/exec"
)

// CLITransport uses gh's credential store without exporting its token. The
// normal client still validates endpoints and applies request and rate budgets.
type CLITransport struct{}

func (CLITransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "api.github.com" {
		return nil, errors.New("GitHub CLI transport only supports GitHub API GET requests")
	}
	args := []string{"api", "--hostname", "github.com", "--method", "GET", "--include", request.URL.RequestURI()}
	for _, name := range []string{"Accept", "X-GitHub-Api-Version", "If-None-Match"} {
		if value := request.Header.Get(name); value != "" {
			args = append(args, "--header", name+": "+value)
		}
	}
	// #nosec G204 G702 -- The executable is fixed; endpoint and header arguments are validated by the client.
	command := exec.CommandContext(request.Context(), "gh", args...)
	output := &limitedOutput{}
	command.Stdout = output
	// gh reports non-2xx statuses on stderr; the included HTTP response below
	// lets the client classify these without exposing credentials or API bodies.
	err := command.Run()
	if output.exceeded || (err != nil && output.Len() == 0) {
		return nil, errors.New("GitHub CLI request failed or exceeded response limit")
	}
	response, parseErr := http.ReadResponse(bufio.NewReader(bytes.NewReader(output.Bytes())), request)
	if parseErr != nil {
		return nil, errors.New("GitHub CLI returned an invalid HTTP response")
	}
	return response, nil
}

type limitedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > maxBodyBytes+64*1024 {
		b.exceeded = true
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(data)
}
