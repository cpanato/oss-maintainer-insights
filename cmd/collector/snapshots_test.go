package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	saved.Policy = snapshot.CatalogPolicy
	saved.SampleAge = &snapshot.SampleAge{Oldest: "2026-01-10T00:00:00Z", Newest: "2026-01-10T00:00:00Z"}
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
	// A fresh legacy sample must not suppress migration to the catalog policy.
	saved.Policy = snapshot.QueryPolicy
	saved.SampleAge = nil
	if err := atomicJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	calls = 0
	output.Reset()
	if err := run(args, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), `"status":"preserved"`) {
		t.Fatal("fresh legacy policy blocked recollection or failure preservation")
	}

	// A fresh dataset using the old methodology must also be recollected.
	saved.Policy = snapshot.CatalogPolicy
	saved.SampleAge = &snapshot.SampleAge{Oldest: "2026-01-10T00:00:00Z", Newest: "2026-01-10T00:00:00Z"}
	saved.Result.MethodologyVersion = "1.0.0"
	saved.Result.ReviewActivity.Sources = nil
	for index := range saved.Result.ReviewActivity.Distribution {
		saved.Result.ReviewActivity.Distribution[index].Sources = nil
	}
	if err := atomicJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	calls = 0
	output.Reset()
	if err := run(args, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), `"status":"preserved"`) {
		t.Fatal("old methodology suppressed command recollection or failure preservation")
	}
	if err := readJSON(path, &after); err != nil {
		t.Fatal(err)
	}
	if after.Result.MethodologyVersion != "1.0.0" {
		t.Fatal("failed migration rewrote legacy data")
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

func TestSnapshotsReservePublicationQuotaAndPersistPendingEnumeration(t *testing.T) {
	directory := t.TempDir()
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`[{"repository":"example/repo"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultClient
	defer func() { http.DefaultClient = original }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: snapshotTransport(func(_ *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Ratelimit-Remaining": []string{"250"}, "X-Ratelimit-Reset": []string{strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)}}, Body: io.NopCloser(strings.NewReader(`{"id":42,"full_name":"example/repo","private":false}`))}, nil
	})}
	var output bytes.Buffer
	if err := run([]string{"snapshots", "--catalog", catalog, "--output", filepath.Join(directory, "public"), "--state", filepath.Join(directory, "state")}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var report manifest
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report.Entries[0].Status != "pending" || report.Entries[0].Reason != "rate_limit" {
		t.Fatalf("publication reserve consumed: calls=%d report=%+v", calls, report)
	}
	var state snapshot.State
	if err := readJSON(filepath.Join(directory, "state", "example--repo.json"), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Pending || state.Progress.EnumerationComplete || state.RepositoryID != 42 {
		t.Fatalf("missing resumable enumeration: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(directory, "public", "example--repo.json")); !os.IsNotExist(err) {
		t.Fatal("unfinished full window was published")
	}
}
