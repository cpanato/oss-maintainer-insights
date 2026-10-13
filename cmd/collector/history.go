package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
	"github.com/cpanato/oss-maintainer-insights/schemas"
)

type historyFiles struct{ directory string }

func (s historyFiles) Read(key string, destination any) error {
	return readJSON(filepath.Join(s.directory, key), destination)
}
func (s historyFiles) Write(key string, value any) error {
	return atomicJSON(filepath.Join(s.directory, key), value)
}

func runHistory(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	catalog := flags.String("catalog", "catalog/repositories.json", "Reviewed repository catalog")
	directory := flags.String("output", "apps/web/public/data/history", "Public monthly aggregates")
	stateDirectory := flags.String("state", ".cache/snapshots/history", "Private per-PR checkpoints")
	requests := flags.Int("requests", 100, "Maximum request attempts per project, including retries")
	maxAge := flags.Duration("max-age", 24*time.Hour, "Skip recent complete monthly series")
	deadline := flags.Duration("timeout", 5*time.Minute, "Deadline for this resumable execution")
	maxWait := flags.Duration("rate-limit-wait", 2*time.Second, "Maximum wait for each API reset")
	useCLI := flags.Bool("github-cli", false, "Use GitHub CLI authentication without exporting a token")
	force := flags.Bool("force", false, "Revalidate completed history even when fresh")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *requests < 0 || *requests > 1300 || *deadline <= 0 || *deadline > 90*time.Minute || *maxAge < 0 || *maxWait < 0 || *maxWait > 65*time.Minute || filepath.Clean(*directory) == filepath.Clean(*stateDirectory) {
		return errors.New("invalid history arguments")
	}
	data, err := os.ReadFile(*catalog)
	if err != nil {
		return err
	}
	if err := schemas.ValidateJSON("catalog", data); err != nil {
		return err
	}
	var entries []snapshot.Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	if len(entries) > 20 {
		return errors.New("historical collection supports at most 20 catalog entries")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		key := strings.ToLower(entry.Repository)
		if seen[key] {
			return errors.New("duplicate catalog repository")
		}
		seen[key] = true
	}
	ctx, cancel := context.WithTimeout(ctx, *deadline)
	defer cancel()
	queuePath := filepath.Join(*stateDirectory, "_queue.json")
	var queue collectionQueue
	if readJSON(queuePath, &queue) != nil {
		queue = collectionQueue{}
	}
	entries, queue = rotateCatalog(entries, queue)
	if err := atomicJSON(queuePath, queue); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	report := manifest{SchemaVersion: 1, AttemptedAt: now.Format(time.RFC3339), Entries: []attempt{}}
	for _, entry := range entries {
		key := strings.ToLower(strings.ReplaceAll(entry.Repository, "/", "--"))
		publicPath := filepath.Join(*directory, key+".json")
		store := historyFiles{directory: filepath.Join(*stateDirectory, key)}
		var prior snapshot.History
		validPrior := readJSON(publicPath, &prior) == nil && schemas.Validate("repository-history", prior) == nil && strings.EqualFold(prior.Repository.FullName, entry.Repository)
		collected, dateErr := time.Parse(time.RFC3339, prior.CollectedAt)
		if validPrior && dateErr == nil && snapshot.HistoryCurrent(store, entry, prior.CollectedAt) && !*force && !now.Before(collected) && now.Sub(collected) < *maxAge {
			report.Entries = append(report.Entries, attempt{Repository: entry.Repository, Status: "fresh"})
			continue
		}
		policy := github.DefaultPolicy()
		policy.MaxRequests = *requests
		policy.MinimumRemaining = 100
		policy.MaxWait = *maxWait
		options := github.Options{Policy: policy, Token: os.Getenv("GITHUB_TOKEN")}
		if *useCLI {
			options.Token = ""
			options.HTTP = &http.Client{Transport: github.CLITransport{}}
		}
		client, err := github.New(options)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Collecting historical reviews for %s (all PR ages, %d attempts)\n", entry.Repository, *requests)
		checkpoint := func(progress snapshot.Progress) error {
			interim := report
			interim.Entries = append(append([]attempt(nil), report.Entries...), attempt{Repository: entry.Repository, Status: "pending", Requests: client.RequestsUsed(), Progress: &progress})
			return atomicJSON(filepath.Join(*directory, "index.json"), interim)
		}
		current, progress, collectErr := snapshot.CollectHistory(ctx, client, entry, now, store, *force, checkpoint)
		status := attempt{Repository: entry.Repository, Status: "updated", Requests: client.RequestsUsed(), Progress: &progress}
		if collectErr != nil {
			var failure *github.Failure
			if !errors.As(collectErr, &failure) {
				return collectErr
			}
			status.Status = "pending"
			status.Reason = failure.Reason
			if failure.Reason == "forbidden" || failure.Reason == "not_found" || failure.Reason == "invalid_response" || failure.Reason == "redirect" {
				status.Status = "unavailable"
				if validPrior {
					status.Status = "preserved"
				}
			}
		} else if err := atomicJSON(publicPath, current); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s history: %s; %d PRs inspected, %d covered\n", entry.Repository, status.Status, progress.EnumeratedPRs, progress.CoveredPRs)
		report.Entries = append(report.Entries, status)
	}
	if err := schemas.Validate("snapshot-index", report); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(*directory, "index.json"), report); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(report)
}
