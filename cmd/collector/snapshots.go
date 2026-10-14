package main

import (
	"bytes"
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

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
	"github.com/cpanato/oss-maintainer-insights/internal/github"
	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
	"github.com/cpanato/oss-maintainer-insights/schemas"
)

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func readJSON(path string, destination any) error {
	// #nosec G304 -- Paths use owner-selected local directories and validated catalog names.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 64*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 64*1024*1024 {
		return errors.New("saved document exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single saved JSON document")
	}
	return nil
}

type attempt struct {
	Repository string             `json:"repository"`
	Status     string             `json:"status"`
	Reason     string             `json:"reason,omitempty"`
	Requests   int                `json:"requests"`
	Progress   *snapshot.Progress `json:"progress,omitempty"`
}
type manifest struct {
	SchemaVersion int       `json:"schema_version"`
	AttemptedAt   string    `json:"attempted_at"`
	Entries       []attempt `json:"entries"`
}

func runSnapshots(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("snapshots", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	catalog := flags.String("catalog", "catalog/repositories.json", "Reviewed public repository catalog")
	directory := flags.String("output", "apps/web/public/data", "Public aggregate output directory")
	stateDirectory := flags.String("state", ".cache/snapshots", "Private conditional checkpoints")
	maxAge := flags.Duration("max-age", 24*time.Hour, "Skip saved samples newer than this age")
	useCLI := flags.Bool("github-cli", false, "Use GitHub CLI authentication without exporting a token")
	maxWait := flags.Duration("rate-limit-wait", 2*time.Second, "Maximum API rate-limit wait; pending collection is checkpointed")
	force := flags.Bool("force", false, "Revalidate samples even when fresh")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *maxAge < 0 || *maxWait < 0 || *maxWait > 65*time.Minute || filepath.Clean(*directory) == filepath.Clean(*stateDirectory) {
		return errors.New("invalid snapshot arguments")
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
	// Divide the 9,000-attempt run cap fairly, with at most 1,300 attempts per project.
	if len(entries) > 20 {
		return errors.New("bounded sample collector supports at most 20 catalog entries")
	}
	names := map[string]bool{}
	for _, entry := range entries {
		key := strings.ToLower(entry.Repository)
		if names[key] {
			return errors.New("duplicate catalog repository")
		}
		names[key] = true
		if _, err := github.RepositoryPath(entry.Repository); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Minute)
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
	now := time.Now().UTC()
	report := manifest{SchemaVersion: 1, AttemptedAt: now.Format(time.RFC3339), Entries: []attempt{}}
	for _, entry := range entries {
		key := strings.ToLower(strings.ReplaceAll(entry.Repository, "/", "--"))
		publicPath := filepath.Join(*directory, key+".json")
		statePath := filepath.Join(*stateDirectory, key+".json")
		var prior snapshot.Public
		validPrior := readJSON(publicPath, &prior) == nil && schemas.Validate("repository-snapshot", prior) == nil && strings.EqualFold(prior.Result.Repository.FullName, entry.Repository)
		var previous snapshot.State
		if err := readJSON(statePath, &previous); err != nil && !os.IsNotExist(err) {
			previous = snapshot.State{}
		}
		// Reuse body-free observations from a legacy sample while enumerating the
		// entire creation window. Its cached streams become part of the pending
		// frozen window; missing PR pages and streams still come from GitHub.
		if validPrior && prior.Complete && prior.Policy != snapshot.CatalogPolicy && prior.Result.MethodologyVersion == analysis.MethodologyVersion && (previous.Version == 2 || previous.Version == 3) && previous.Policy == prior.Policy && strings.EqualFold(previous.Repository, entry.Repository) {
			previous.Policy = snapshot.CatalogPolicy
			previous.Pending = true
			previous.ObservedAt = prior.Result.CollectedAt
			previous.RepositoryID = prior.Result.Repository.ID
			previous.MethodologyVersion = analysis.MethodologyVersion
			previous.BotIDs = append([]int64(nil), entry.BotIDs...)
		}
		collected, parseErr := time.Parse(time.RFC3339Nano, prior.Result.CollectedAt)
		if validPrior && prior.Policy == snapshot.CatalogPolicy && prior.Result.MethodologyVersion == analysis.MethodologyVersion && parseErr == nil && !previous.Pending && !*force && !now.Before(collected) && now.Sub(collected) < *maxAge {
			report.Entries = append(report.Entries, attempt{Repository: entry.Repository, Status: "fresh"})
			continue
		}
		policy := github.DefaultPolicy()
		policy.MaxRequests = min(1300, 9000/len(entries))
		policy.MaxWait = *maxWait
		policy.MinimumRemaining = 250
		fmt.Fprintf(os.Stderr, "Collecting %s (all eligible PRs in 90 days, %d request attempts)\n", entry.Repository, policy.MaxRequests)
		options := github.Options{Policy: policy, Token: os.Getenv("GITHUB_TOKEN"), Sleep: func(ctx context.Context, delay time.Duration) error {
			if delay > 2*time.Second {
				fmt.Fprintf(os.Stderr, "Waiting %s for GitHub rate limit/retry; collection can be cancelled and resumed\n", delay.Round(time.Second))
			}
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		if *useCLI {
			options.Token = ""
			options.HTTP = &http.Client{Transport: github.CLITransport{}}
		}
		client, err := github.New(options)
		if err != nil {
			return err
		}
		var checkpointErr error
		checkpointCount := 0
		checkpoint := func(state snapshot.State) error {
			checkpointCount++
			if checkpointCount%25 != 0 {
				return nil
			}
			checkpointErr = atomicJSON(statePath, state)
			if checkpointErr != nil {
				return checkpointErr
			}
			interim := report
			interim.Entries = append(append([]attempt(nil), report.Entries...), attempt{Repository: entry.Repository, Status: "pending", Requests: client.RequestsUsed(), Progress: state.Progress})
			checkpointErr = atomicJSON(filepath.Join(*directory, "index.json"), interim)
			if checkpointCount%100 == 0 {
				fmt.Fprintf(os.Stderr, "Checkpoint %s: %d saved pages, %d attempts this run\n", entry.Repository, len(state.Pages), client.RequestsUsed())
			}
			return checkpointErr
		}
		collect := snapshot.CollectCatalogIncrementalWithCheckpoint
		if *force {
			collect = snapshot.CollectCatalogWithCheckpoint
		}
		current, state, collectErr := collect(ctx, client, entry, time.Time{}, previous, checkpoint)
		if checkpointErr != nil {
			return checkpointErr
		}
		if collectErr != nil {
			var failure *github.Failure
			if !errors.As(collectErr, &failure) || (failure.Reason != "budget" && failure.Reason != "rate_limit" && failure.Reason != "cancelled" && failure.Reason != "network" && failure.Reason != "timeout" && failure.Reason != "http") {
				state.Pending = false
			}
		}
		if err := atomicJSON(statePath, state); err != nil {
			return err
		}
		status := attempt{Repository: entry.Repository, Status: "updated", Requests: client.RequestsUsed(), Progress: state.Progress}
		if collectErr != nil {
			status.Status = "unavailable"
			if validPrior {
				status.Status = "preserved"
			}
			var failure *github.Failure
			if errors.As(collectErr, &failure) {
				status.Reason = failure.Reason
				if state.Pending {
					status.Status = "pending"
				}
			} else {
				status.Reason = "invalid_response"
			}
		} else {
			if !current.Complete {
				status.Status = "pending"
				status.Reason = "partial"
			} else if err := atomicJSON(publicPath, current); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "%s: %s (%d attempts)\n", entry.Repository, status.Status, status.Requests)
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
