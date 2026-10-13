package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
)

func TestHistoricalCLIResumesPublishesOnlyCompleteAggregatesAndPreservesThemOnFailure(t *testing.T) {
	directory := t.TempDir()
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`[{"repository":"example/repo","group":"Community"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultClient
	defer func() { http.DefaultClient = previous }()
	calls := []string{}
	fail := false
	date := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	http.DefaultClient = &http.Client{Transport: snapshotTransport(func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.URL.Path)
		body := `[]`
		status := http.StatusOK
		switch {
		case fail:
			status, body = http.StatusNotFound, `{"message":"Not Found"}`
		case strings.HasPrefix(request.URL.Path, "/repos/"):
			body = `{"id":42,"full_name":"example/repo","private":false}`
		case request.URL.Path == "/repositories/42/pulls":
			body = `[{"id":1,"number":1,"user":{"id":101,"login":"author","type":"User"},"created_at":"2020-01-01T00:00:00Z","draft":false,"state":"closed"}]`
		case strings.HasSuffix(request.URL.Path, "/reviews"):
			body = `[{"id":71,"user":{"id":201,"login":"reviewer","type":"User"},"submitted_at":"` + date + `","state":"APPROVED"}]`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	public := filepath.Join(directory, "public")
	private := filepath.Join(directory, "private")
	args := []string{"history", "--catalog", catalog, "--output", public, "--state", private}
	publicPath := filepath.Join(public, "example--repo.json")
	var output bytes.Buffer
	if err := run(append(args, "--requests=2"), strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(publicPath); !os.IsNotExist(err) {
		t.Fatal("published incomplete history")
	}
	output.Reset()
	calls = nil
	if err := run(append(args, "--requests=10"), strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var history snapshot.History
	if err := readJSON(publicPath, &history); err != nil || history.Progress.CoveredPRs != 1 || len(calls) != 4 {
		t.Fatalf("resumption: %+v, %v, %v", history, err, calls)
	}
	encoded, _ := json.Marshal(history)
	if strings.Contains(string(encoded), "occurred_at") || strings.Contains(string(encoded), `"pages"`) {
		t.Fatal("source events in public history")
	}
	calls = nil
	if err := run(args, strings.NewReader(""), &output); err != nil || len(calls) != 0 {
		t.Fatal("fresh history should not fetch GitHub")
	}
	// #nosec G304 -- This path is inside the test's temporary directory.
	before, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := run(append(args, "--force"), strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- This path is inside the test's temporary directory.
	after, err := os.ReadFile(publicPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed history refresh replaced a complete series")
	}
	var report manifest
	if err := readJSON(filepath.Join(public, "index.json"), &report); err != nil || report.Entries[0].Status != "preserved" {
		t.Fatal("missing preserved-data status")
	}
}
