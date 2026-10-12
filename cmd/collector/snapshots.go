package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	data, err := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 8*1024*1024 {
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
	Repository string `json:"repository"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Requests   int    `json:"requests"`
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
	force := flags.Bool("force", false, "Revalidate samples even when fresh")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *maxAge < 0 || filepath.Clean(*directory) == filepath.Clean(*stateDirectory) {
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
	// Twenty reviewed entries bound a run to 600 requests, below the 700-request run budget.
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	report := manifest{SchemaVersion: 1, AttemptedAt: now.Format(time.RFC3339), Entries: []attempt{}}
	for _, entry := range entries {
		key := strings.ToLower(strings.ReplaceAll(entry.Repository, "/", "--"))
		publicPath := filepath.Join(*directory, key+".json")
		statePath := filepath.Join(*stateDirectory, key+".json")
		var prior snapshot.Public
		validPrior := readJSON(publicPath, &prior) == nil && schemas.Validate("repository-snapshot", prior) == nil && strings.EqualFold(prior.Result.Repository.FullName, entry.Repository)
		collected, parseErr := time.Parse(time.RFC3339Nano, prior.Result.CollectedAt)
		if validPrior && parseErr == nil && !*force && !now.Before(collected) && now.Sub(collected) < *maxAge {
			report.Entries = append(report.Entries, attempt{Repository: entry.Repository, Status: "fresh"})
			continue
		}
		var previous snapshot.State
		if err := readJSON(statePath, &previous); err != nil && !os.IsNotExist(err) {
			previous = snapshot.State{}
		}
		policy := github.DefaultPolicy()
		policy.MaxRequests = 30
		client, err := github.New(github.Options{Policy: policy, Token: os.Getenv("GITHUB_TOKEN")})
		if err != nil {
			return err
		}
		current, state, collectErr := snapshot.Collect(ctx, client, entry, time.Time{}, previous)
		status := attempt{Repository: entry.Repository, Status: "updated", Requests: client.RequestsUsed()}
		if collectErr != nil {
			status.Status = "unavailable"
			if validPrior {
				status.Status = "preserved"
			}
			var failure *github.Failure
			if errors.As(collectErr, &failure) {
				status.Reason = failure.Reason
			} else {
				status.Reason = "invalid_response"
			}
		} else {
			if err := atomicJSON(statePath, state); err != nil {
				return err
			}
			if validPrior && prior.Complete && !current.Complete {
				status.Status = "preserved"
				status.Reason = "partial"
			} else if err := atomicJSON(publicPath, current); err != nil {
				return err
			}
		}
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
