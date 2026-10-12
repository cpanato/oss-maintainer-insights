package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestAnalyze(t *testing.T) {
	data, err := os.ReadFile("../../tests/fixtures/monthly-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"analyze"}, bytes.NewReader(data), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"methodology_version":"1.1.0"`) {
		t.Fatal("missing version")
	}
}
func TestRejectsInvalidDocuments(t *testing.T) {
	for _, document := range []string{`{}`, `{"body":"forbidden"}`, `{} {}`, `broken`} {
		if err := run([]string{"analyze"}, strings.NewReader(document), &bytes.Buffer{}); err == nil {
			t.Fatal("expected failure")
		}
	}
	if err := run(nil, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error")
	}
}

func TestProbeArgumentsAndZeroBudget(t *testing.T) {
	for _, args := range [][]string{{"probe"}, {"probe", "--requests=-1", "a/b"}, {"probe", "--requests=101", "a/b"}, {"probe", "https://evil.test/repo"}, {"probe", "a/b", "c/d", "e/f", "g/h"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	var output bytes.Buffer
	if err := run([]string{"probe", "--requests=0", "example/project"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"failure_reason":"budget"`) || !strings.Contains(output.String(), `"requests":0`) {
		t.Fatalf("unexpected zero-budget probe: %s", output.String())
	}
}
