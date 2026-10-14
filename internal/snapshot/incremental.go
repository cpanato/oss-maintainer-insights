package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
)

const reconciliationInterval = 7 * 24 * time.Hour

// Version 1.2 changes PR eligibility, not event normalization. Complete unchanged
// 1.1 streams can be reinterpreted by the new engine; 1.0 lacks Prow signals.
func compatibleEventMethodology(previous string) bool {
	return previous == analysis.MethodologyVersion || (previous == "1.1.0" && analysis.MethodologyVersion == "1.2.0")
}

// TrackedPR records the metadata used to decide when cached event streams need
// revalidation. It is private collection state, never a published source row.
type TrackedPR struct {
	ID        int64  `json:"id"`
	UpdatedAt string `json:"updated_at"`
	CheckedAt string `json:"checked_at"`
	State     string `json:"state"`
}

// Collection distinguishes a complete PR population from event freshness.
type Collection struct {
	Mode               string `json:"mode"`
	RefreshedPRs       int    `json:"refreshed_prs"`
	ReusedPRs          int    `json:"reused_prs"`
	EventsCheckedSince string `json:"events_checked_since,omitempty"`
	ReconciliationDays int    `json:"reconciliation_interval_days"`
}

// CollectCatalogIncrementalWithCheckpoint revalidates new, changed, and open PRs
// while retaining unchanged closed streams until their reconciliation deadline.
func CollectCatalogIncrementalWithCheckpoint(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State, checkpoint func(State) error) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, true, true, true, checkpoint)
}

func cachedPRUpdates(page Page) bool {
	var records []rawPR
	if json.Unmarshal(page.Value, &records) != nil || records == nil {
		return false
	}
	for _, record := range records {
		if record.Updated == "" {
			return false
		}
	}
	return true
}

func (r *reader) reuseClosedPR(pr rawPR, path string, observed time.Time) bool {
	if !r.incremental || r.resume || pr.State != "closed" || pr.Updated == "" || r.previous.RepositoryID != r.next.RepositoryID || !compatibleEventMethodology(r.previous.MethodologyVersion) {
		return false
	}
	// Changed exclusion rules must not reuse a previous interpretation of events.
	if !slices.Equal(r.previous.BotIDs, r.next.BotIDs) {
		return false
	}
	prior, ok := r.previous.Tracked[pr.Number]
	checked, err := timestamp(prior.CheckedAt)
	if !ok || err != nil || prior.ID != pr.ID || prior.State != "closed" || prior.UpdatedAt != pr.Updated || observed.Before(checked) || observed.Sub(checked) >= reconciliationInterval {
		return false
	}
	roots := []string{
		fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", path, pr.Number),
		fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, pr.Number),
		fmt.Sprintf("%s/pulls/%d/comments?per_page=100", path, pr.Number),
	}
	for index, root := range roots {
		if !r.cachedStream(root, index == 0) {
			return false
		}
	}
	for _, root := range roots {
		for endpoint := root; endpoint != ""; endpoint = r.previous.Pages[endpoint].Next {
			r.replay[endpoint] = true
		}
	}
	return true
}

func (r *reader) collectionSummary(prs []rawPR, observed time.Time) *Collection {
	result := &Collection{Mode: "incremental", ReconciliationDays: 7}
	var oldest time.Time
	for _, pr := range prs {
		tracked, ok := r.next.Tracked[pr.Number]
		checked, err := timestamp(tracked.CheckedAt)
		if !ok || err != nil {
			continue
		}
		if checked.Equal(observed) {
			result.RefreshedPRs++
		} else {
			result.ReusedPRs++
		}
		if oldest.IsZero() || checked.Before(oldest) {
			oldest = checked
		}
	}
	if !oldest.IsZero() {
		result.EventsCheckedSince = oldest.Format(time.RFC3339)
	}
	return result
}
