package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
	"github.com/cpanato/oss-maintainer-insights/internal/github"
	"github.com/cpanato/oss-maintainer-insights/schemas"
)

const HistoryPolicy = "all-pr-review-history-90-days-v1"

// HistoryStore stores small, sanitized listing and per-PR checkpoints separately.
// Read must return fs.ErrNotExist for a missing key; Write must replace atomically.
type HistoryStore interface {
	Read(string, any) error
	Write(string, any) error
}

type History struct {
	Population    *Population         `json:"population,omitempty"`
	SchemaVersion int                 `json:"schema_version"`
	Policy        string              `json:"policy"`
	Repository    analysis.Repository `json:"repository"`
	CollectedAt   string              `json:"collected_at"`
	Window        analysis.Window     `json:"window"`
	Progress      Progress            `json:"progress"`
	Collection    Collection          `json:"collection"`
	Months        []analysis.Result   `json:"months"`
}

type historyCursor struct {
	Generation         int      `json:"generation"`
	Version            int      `json:"version"`
	Repository         string   `json:"repository"`
	RepositoryID       int64    `json:"repository_id"`
	ObservedAt         string   `json:"observed_at"`
	MethodologyVersion string   `json:"methodology_version"`
	BotIDs             []int64  `json:"bot_ids"`
	Page               int      `json:"page"`
	Record             int      `json:"record"`
	Pending            bool     `json:"pending"`
	Progress           Progress `json:"progress"`
}

type historyListing struct {
	Generation int    `json:"generation"`
	ObservedAt string `json:"observed_at"`
	Page       Page   `json:"page"`
}

type historyPR struct {
	Generation int                  `json:"generation"`
	ObservedAt string               `json:"observed_at"`
	Raw        rawPR                `json:"pr"`
	State      State                `json:"state"`
	Input      analysis.PullRequest `json:"input"`
}

func historyRead(store HistoryStore, key string, value any) error {
	err := store.Read(key, value)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// HistoryCurrent ensures a fresh public result still matches completed private
// state and the current exclusion rules before skipping network acquisition.
func HistoryCurrent(store HistoryStore, entry Entry, collectedAt string) bool {
	var cursor historyCursor
	return store.Read("cursor.json", &cursor) == nil && cursor.Version == 1 && !cursor.Pending && cursor.ObservedAt == collectedAt && strings.EqualFold(cursor.Repository, entry.Repository) && cursor.MethodologyVersion == analysis.MethodologyVersion && slices.Equal(cursor.BotIDs, entry.BotIDs)
}

// CollectHistory exhaustively scans every PR, including old PRs with reviews in
// the target window. A cursor advances only after all three event streams finish.
// Budget failures return progress without publishing an incomplete monthly series.
func CollectHistory(ctx context.Context, client Getter, entry Entry, observed time.Time, store HistoryStore, force bool, progress func(Progress) error) (History, Progress, error) {
	observed = observed.UTC().Truncate(time.Second)
	if observed.IsZero() {
		observed = time.Now().UTC().Truncate(time.Second)
	}
	var cursor historyCursor
	if err := historyRead(store, "cursor.json", &cursor); err != nil {
		return History{}, Progress{}, err
	}
	old, err := timestamp(cursor.ObservedAt)
	compatible := cursor.Version == 1 && cursor.Generation > 0 && cursor.Progress.Population != nil && strings.EqualFold(cursor.Repository, entry.Repository) && cursor.MethodologyVersion == analysis.MethodologyVersion && slices.Equal(cursor.BotIDs, entry.BotIDs) && err == nil && !observed.Before(old)
	if !compatible || !cursor.Pending {
		cursor = historyCursor{Generation: cursor.Generation + 1, Version: 1, Repository: entry.Repository, ObservedAt: observed.Format(time.RFC3339), MethodologyVersion: analysis.MethodologyVersion, BotIDs: append([]int64{}, entry.BotIDs...), Page: 1, Pending: true, Progress: Progress{Population: &Population{}, Window: analysis.Window{Start: observed.Add(-90 * 24 * time.Hour).Format(time.RFC3339), End: observed.Format(time.RFC3339)}}}
	} else {
		observed = old
	}
	if cursor.Page < 1 || cursor.Record < 0 || cursor.Record > 100 {
		return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
	}
	persist := func() error {
		if err := store.Write("cursor.json", cursor); err != nil {
			return err
		}
		if progress != nil {
			return progress(cursor.Progress)
		}
		return nil
	}
	path, err := github.RepositoryPath(entry.Repository)
	if err != nil {
		return History{}, cursor.Progress, err
	}
	var metadata struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	if _, err := client.GetConditional(ctx, path, &metadata, ""); err != nil {
		return History{}, cursor.Progress, err
	}
	if metadata.ID <= 0 || metadata.ID > 9007199254740991 || metadata.Private == nil || *metadata.Private || (cursor.RepositoryID != 0 && cursor.RepositoryID != metadata.ID) {
		return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
	}
	if _, err := github.RepositoryPath(metadata.FullName); err != nil {
		return History{}, cursor.Progress, err
	}
	cursor.RepositoryID = metadata.ID
	if err := persist(); err != nil {
		return History{}, cursor.Progress, err
	}
	path = "/repositories/" + strconv.FormatInt(metadata.ID, 10)
	seen := map[int64]bool{}
	// Rebuild identities from completed pages instead of rewriting a growing
	// repository-wide record map after every PR.
	for page := 1; page < cursor.Page; page++ {
		var listing historyListing
		if err := store.Read(fmt.Sprintf("pages/%d.json", page), &listing); err != nil || listing.Generation != cursor.Generation || listing.ObservedAt != cursor.ObservedAt {
			return History{}, cursor.Progress, errors.New("missing historical listing checkpoint")
		}
		var records []rawPR
		if err := json.Unmarshal(listing.Page.Value, &records); err != nil {
			return History{}, cursor.Progress, err
		}
		for _, record := range records {
			seen[record.ID] = true
		}
	}
	for !cursor.Progress.EnumerationComplete {
		key := fmt.Sprintf("pages/%d.json", cursor.Page)
		endpoint := fmt.Sprintf("%s/pulls?state=all&sort=created&direction=desc&per_page=100&page=%d", path, cursor.Page)
		var listing historyListing
		if err := historyRead(store, key, &listing); err != nil {
			return History{}, cursor.Progress, err
		}
		r := reader{client: client, resume: listing.Generation == cursor.Generation && listing.ObservedAt == cursor.ObservedAt, previous: State{Pages: map[string]Page{endpoint: listing.Page}}, next: State{Pages: map[string]Page{}}}
		var records []rawPR
		next, err := r.get(ctx, endpoint, &records)
		if err != nil {
			return History{}, cursor.Progress, err
		}
		if records == nil || len(records) > 100 || cursor.Record > len(records) || (len(records) == 0 && next != "") {
			return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
		}
		if next != "" {
			nextURL, err := url.Parse(next)
			if err != nil || nextURL.Path != path+"/pulls" || nextURL.Query().Get("page") != strconv.Itoa(cursor.Page+1) || nextURL.Query().Get("state") != "all" || nextURL.Query().Get("sort") != "created" || nextURL.Query().Get("direction") != "desc" || nextURL.Query().Get("per_page") != "100" {
				return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
			}
		}
		listing = historyListing{Generation: cursor.Generation, ObservedAt: cursor.ObservedAt, Page: r.next.Pages[endpoint]}
		if err := store.Write(key, listing); err != nil {
			return History{}, cursor.Progress, err
		}
		for _, record := range records[:cursor.Record] {
			seen[record.ID] = true
		}
		for cursor.Record < len(records) {
			raw := records[cursor.Record]
			_, actorErr := actor(raw.User)
			created, createdErr := timestamp(raw.Created)
			if raw.ID <= 0 || raw.ID > 9007199254740991 || raw.Number <= 0 || raw.Draft == nil || (raw.State != "open" && raw.State != "closed") || actorErr != nil || createdErr != nil {
				return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
			}
			if raw.Updated != "" {
				updated, err := timestamp(raw.Updated)
				if err != nil || updated.Before(created) {
					return History{}, cursor.Progress, &github.Failure{Reason: "invalid_response"}
				}
			}
			if raw.Merged != nil {
				if _, err := timestamp(*raw.Merged); err != nil {
					return History{}, cursor.Progress, err
				}
			}
			if !seen[raw.ID] {
				population := *cursor.Progress.Population
				eligible, err := population.record(raw, time.Time{}, observed, entry.BotIDs)
				if err != nil {
					return History{}, cursor.Progress, err
				}
				if eligible {
					if err := historyEvents(ctx, client, entry, path, raw, observed, cursor.Generation, store, force); err != nil {
						return History{}, cursor.Progress, err
					}
					cursor.Progress.EligiblePRs++
					cursor.Progress.CoveredPRs++
				}
				cursor.Progress.Population = &population
				cursor.Progress.EnumeratedPRs++
				seen[raw.ID] = true
			}
			cursor.Record++
			if err := persist(); err != nil {
				return History{}, cursor.Progress, err
			}
		}
		if next == "" {
			cursor.Progress.EnumerationComplete = true
		} else {
			cursor.Page++
			cursor.Record = 0
		}
		if err := persist(); err != nil {
			return History{}, cursor.Progress, err
		}
	}
	public, err := historyAggregate(entry, metadata.ID, metadata.FullName, cursor, store)
	if err != nil {
		return History{}, cursor.Progress, err
	}
	cursor.Pending = false
	if err := persist(); err != nil {
		return History{}, cursor.Progress, err
	}
	return public, cursor.Progress, nil
}

func historyEvents(ctx context.Context, client Getter, entry Entry, path string, raw rawPR, observed time.Time, generation int, store HistoryStore, force bool) error {
	key := fmt.Sprintf("prs/%d.json", raw.Number)
	var shard historyPR
	if err := historyRead(store, key, &shard); err != nil {
		return err
	}
	compatible := shard.State.RepositoryID == 0 || (shard.State.RepositoryID == mustRepositoryID(path) && shard.Raw.ID == raw.ID && compatibleEventMethodology(shard.State.MethodologyVersion) && slices.Equal(shard.State.BotIDs, entry.BotIDs))
	if !compatible {
		shard = historyPR{}
	}
	state := State{Version: 3, Policy: HistoryPolicy, Repository: entry.Repository, RepositoryID: mustRepositoryID(path), MethodologyVersion: analysis.MethodologyVersion, BotIDs: entry.BotIDs, Pages: map[string]Page{}, Tracked: map[int]TrackedPR{}}
	resume := shard.Generation == generation && shard.ObservedAt == observed.Format(time.RFC3339)
	r := reader{client: client, previous: shard.State, next: state, resume: resume, incremental: !force, replay: map[string]bool{}}
	r.checkpoint = func(state State) error {
		return store.Write(key, historyPR{Generation: generation, ObservedAt: observed.Format(time.RFC3339), Raw: raw, State: state, Input: shard.Input})
	}
	reused := r.reuseClosedPR(raw, path, observed)
	user, _ := actor(raw.User)
	outcome := "open"
	if raw.Merged != nil {
		outcome = "merged"
	} else if raw.State == "closed" {
		outcome = "closed_unmerged"
	}
	input := analysis.PullRequest{ID: raw.ID, Author: user, CreatedAt: raw.Created, Outcome: outcome, Reviews: []analysis.Review{}, IssueComments: []analysis.Event{}, ReviewComments: []analysis.Event{}}
	for _, stream := range []string{"reviews", "issue_comments", "review_comments"} {
		endpoint := fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", path, raw.Number)
		switch stream {
		case "issue_comments":
			endpoint = fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, raw.Number)
		case "review_comments":
			endpoint = fmt.Sprintf("%s/pulls/%d/comments?per_page=100", path, raw.Number)
		}
		events, complete, err := r.events(ctx, endpoint, stream == "reviews")
		if err != nil {
			if saveErr := r.checkpoint(r.next); saveErr != nil {
				return saveErr
			}
			return err
		}
		if !complete {
			return &github.Failure{Reason: "invalid_response"}
		}
		if stream == "reviews" {
			input.Reviews = events
			input.Coverage.Reviews = true
		} else {
			values := []analysis.Event{}
			for _, event := range events {
				values = append(values, event.Event)
			}
			if stream == "issue_comments" {
				input.IssueComments = values
				input.Coverage.IssueComments = true
			} else {
				input.ReviewComments = values
				input.Coverage.ReviewComments = true
			}
		}
	}
	checked := observed.Format(time.RFC3339)
	if reused || resume {
		if prior, ok := shard.State.Tracked[raw.Number]; ok {
			checked = prior.CheckedAt
		}
	}
	r.next.Tracked[raw.Number] = TrackedPR{ID: raw.ID, State: raw.State, UpdatedAt: raw.Updated, CheckedAt: checked}
	return store.Write(key, historyPR{Generation: generation, ObservedAt: observed.Format(time.RFC3339), Raw: raw, State: r.next, Input: input})
}

func mustRepositoryID(path string) int64 {
	id, _ := strconv.ParseInt(strings.TrimPrefix(path, "/repositories/"), 10, 64)
	return id
}

func historyAggregate(entry Entry, id int64, name string, cursor historyCursor, store HistoryStore) (History, error) {
	input := analysis.Input{SchemaVersion: 1, Repository: analysis.Repository{ID: id, FullName: name}, Mode: "historical", ObservedAt: cursor.ObservedAt, BotIDs: append([]int64{}, entry.BotIDs...), Enumeration: analysis.Enumeration{CreationCohort: true, ReviewActivity: true}, PullRequests: []analysis.PullRequest{}}
	collection := Collection{Mode: "incremental", ReconciliationDays: 7}
	seen := map[int64]bool{}
	population := &Population{}
	observed, _ := timestamp(cursor.ObservedAt)
	for page := 1; page <= cursor.Page; page++ {
		var listing historyListing
		if err := store.Read(fmt.Sprintf("pages/%d.json", page), &listing); err != nil || listing.Generation != cursor.Generation || listing.ObservedAt != cursor.ObservedAt {
			return History{}, errors.New("missing historical listing checkpoint")
		}
		var records []rawPR
		if err := json.Unmarshal(listing.Page.Value, &records); err != nil {
			return History{}, err
		}
		for _, raw := range records {
			if seen[raw.ID] {
				continue
			}
			seen[raw.ID] = true
			included, err := population.record(raw, time.Time{}, observed, entry.BotIDs)
			if err != nil {
				return History{}, err
			}
			if !included {
				continue
			}
			var shard historyPR
			if err := historyRead(store, fmt.Sprintf("prs/%d.json", raw.Number), &shard); err != nil {
				return History{}, err
			}
			if shard.Generation != cursor.Generation || shard.ObservedAt != cursor.ObservedAt || shard.Raw.ID != raw.ID {
				continue
			}
			input.PullRequests = append(input.PullRequests, shard.Input)
			checked := shard.State.Tracked[raw.Number].CheckedAt
			if checked == cursor.ObservedAt {
				collection.RefreshedPRs++
			} else {
				collection.ReusedPRs++
			}
			if collection.EventsCheckedSince == "" || checked < collection.EventsCheckedSince {
				collection.EventsCheckedSince = checked
			}
		}
	}
	if len(input.PullRequests) != cursor.Progress.EligiblePRs {
		return History{}, errors.New("historical event coverage does not match enumeration")
	}
	public := History{Population: population, SchemaVersion: 1, Policy: HistoryPolicy, Repository: input.Repository, CollectedAt: cursor.ObservedAt, Window: cursor.Progress.Window, Progress: cursor.Progress, Collection: collection, Months: []analysis.Result{}}
	start, _ := timestamp(public.Window.Start)
	end, _ := timestamp(public.Window.End)
	for start.Before(end) {
		next := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		if next.After(end) {
			next = end
		}
		input.Window = analysis.Window{Start: start.Format(time.RFC3339), End: next.Format(time.RFC3339)}
		if err := schemas.Validate("analysis-input", input); err != nil {
			return History{}, err
		}
		result, err := analysis.Analyze(input)
		if err != nil {
			return History{}, err
		}
		public.Months = append(public.Months, result)
		start = next
	}
	if err := schemas.Validate("repository-history", public); err != nil {
		return History{}, err
	}
	return public, nil
}
