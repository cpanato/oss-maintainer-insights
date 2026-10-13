package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
)

func TestCollectionQueueRotatesStartAndPreservesEachProject(t *testing.T) {
	entries := []snapshot.Entry{{Repository: "org/one"}, {Repository: "org/two"}, {Repository: "org/three"}}
	queue := collectionQueue{}
	for index := range 6 {
		rotated, next := rotateCatalog(entries, queue)
		if rotated[0].Repository != entries[index%3].Repository || len(rotated) != len(entries) {
			t.Fatal("queue failed to rotate fairly")
		}
		seen := map[string]bool{}
		for _, entry := range rotated {
			if seen[entry.Repository] {
				t.Fatal("duplicate project")
			}
			seen[entry.Repository] = true
		}
		queue = next
	}
	if !reflect.DeepEqual(entries, []snapshot.Entry{{Repository: "org/one"}, {Repository: "org/two"}, {Repository: "org/three"}}) {
		t.Fatal("rotation mutated the catalog")
	}
}

func TestCollectionQueueHandlesCatalogChangesAndInvalidState(t *testing.T) {
	entries := []snapshot.Entry{{Repository: "org/one"}, {Repository: "org/two"}}
	for _, queue := range []collectionQueue{{Version: 1, NextRepository: "removed/project"}, {Version: 2, NextRepository: "org/two"}} {
		rotated, _ := rotateCatalog(entries, queue)
		if rotated[0].Repository != "org/one" {
			t.Fatal("invalid queue changed the starting project")
		}
	}
	rotated, _ := rotateCatalog(entries, collectionQueue{Version: 1, NextRepository: "ORG/TWO"})
	if rotated[0].Repository != "org/two" {
		t.Fatal("canonical case mismatch")
	}
	_, next := rotateCatalog(entries[:1], collectionQueue{})
	if next.NextRepository != "org/one" {
		t.Fatal("single-project queue")
	}
	empty, _ := rotateCatalog(nil, collectionQueue{})
	if len(empty) != 0 {
		t.Fatal("empty catalog")
	}
}

func TestCollectionQueuePersistsAcrossFailedRuns(t *testing.T) {
	directory := t.TempDir()
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`[{"repository":"org/one","group":"Community"},{"repository":"org/two","group":"Community"},{"repository":"org/three","group":"Community"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultClient
	defer func() { http.DefaultClient = previous }()
	calls := []string{}
	http.DefaultClient = &http.Client{Transport: snapshotTransport(func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.URL.Path)
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}, nil
	})}
	args := []string{"snapshots", "--catalog", catalog, "--output", filepath.Join(directory, "public"), "--state", filepath.Join(directory, "private")}
	for index, expected := range [][]string{{"/repos/org/one", "/repos/org/two", "/repos/org/three"}, {"/repos/org/two", "/repos/org/three", "/repos/org/one"}} {
		var output bytes.Buffer
		calls = nil
		if err := run(args, strings.NewReader(""), &output); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, expected) {
			t.Fatalf("execution %d: %v", index, calls)
		}
		var queue collectionQueue
		if err := readJSON(filepath.Join(directory, "private", "_queue.json"), &queue); err != nil || queue.Version != 1 {
			t.Fatalf("queue not persisted: %v", err)
		}
	}
}
