package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
)

type snapshotTransport func(*http.Request) (*http.Response, error)

func (f snapshotTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
func TestSnapshotsPreserveCompleteOnFailureAndSkipFresh(t *testing.T) {
	directory := t.TempDir()
	publicDir := filepath.Join(directory, "public")
	stateDir := filepath.Join(directory, "state")
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`[{"repository":"example/repo","group":"Community"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/live-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved snapshot.Public
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Result.CollectedAt = time.Now().UTC().Format(time.RFC3339)
	path := filepath.Join(publicDir, "example--repo.json")
	if err := atomicJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	before := saved
	original := http.DefaultClient
	defer func() { http.DefaultClient = original }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: snapshotTransport(func(_ *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}, nil
	})}
	args := []string{"snapshots", "--catalog", catalog, "--output", publicDir, "--state", stateDir}
	var output bytes.Buffer
	if err := run(args, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || !strings.Contains(output.String(), `"status":"fresh"`) {
		t.Fatal("fresh snapshot caused network access")
	}
	output.Reset()
	if err := run(append(args, "--force"), strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var after snapshot.Public
	if err := readJSON(path, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || calls != 1 || !strings.Contains(output.String(), `"status":"preserved"`) {
		t.Fatal("failed update replaced complete snapshot")
	}
}
func TestSnapshotsRejectDuplicateCatalogWithoutNetwork(t *testing.T) {
	directory := t.TempDir()
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`[{"repository":"example/repo"},{"repository":"EXAMPLE/REPO"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"snapshots", "--catalog", catalog, "--output", filepath.Join(directory, "public"), "--state", filepath.Join(directory, "state")}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("duplicate catalog accepted")
	}
}
