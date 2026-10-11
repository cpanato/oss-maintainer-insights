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
	if !strings.Contains(output.String(), `"methodology_version":"1.0.0"`) {
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
