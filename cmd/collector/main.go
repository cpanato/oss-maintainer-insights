// The collector exposes offline analysis and bounded public-API feasibility probes.
// Historical ingestion and checkpoint persistence follow in a later milestone.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
	"github.com/cpanato/oss-maintainer-insights/internal/github"
	"github.com/cpanato/oss-maintainer-insights/internal/probe"
)

func run(args []string, input io.Reader, output io.Writer) error {
	return runContext(context.Background(), args, input, output)
}

func runContext(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) > 0 && args[0] == "probe" {
		return runProbe(ctx, args[1:], output)
	}
	if len(args) != 1 || args[0] != "analyze" {
		return fmt.Errorf("usage: collector analyze < normalized-input.json | collector probe [--requests=10] owner/repo [owner/repo]")
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

func runProbe(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	requests := flags.Int("requests", 10, "Maximum requests per repository, including retries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *requests < 0 || *requests > 100 || flags.NArg() < 1 || flags.NArg() > 3 {
		return fmt.Errorf("probe requires 1–3 repositories and a request cap from 0 to 100")
	}
	for _, repository := range flags.Args() {
		if err := probe.ValidateRepository(repository); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	token := os.Getenv("GITHUB_TOKEN")
	observed := time.Now().UTC()
	for _, repository := range flags.Args() {
		policy := github.DefaultPolicy()
		policy.MaxRequests = *requests
		client, err := github.New(github.Options{Policy: policy, Token: token})
		if err != nil {
			return err
		}
		if err := json.NewEncoder(output).Encode(probe.Run(ctx, client, repository, observed, token != "")); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
