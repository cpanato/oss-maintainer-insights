// The collector currently exposes offline analysis of normalized fixtures.
// Authenticated collection and checkpoint persistence follow in a later milestone.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
)

func run(args []string, input io.Reader, output io.Writer) error {
	if len(args) != 1 || args[0] != "analyze" {
		return fmt.Errorf("usage: collector analyze < normalized-input.json")
	}
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	var normalized analysis.Input
	if err := decoder.Decode(&normalized); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected a single JSON document")
	}
	result, err := analysis.Analyze(normalized)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
