package analysis

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T) Input {
	t.Helper()
	data, err := os.ReadFile("../../tests/fixtures/monthly-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input Input
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	return input
}
func TestSharedGolden(t *testing.T) {
	result, err := Analyze(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../tests/fixtures/monthly-expected.json")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(expected, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("result differs from shared golden:\n%s", actual)
	}
}
func TestIncompleteStreams(t *testing.T) {
	input := fixture(t)
	input.PullRequests[0].Coverage.Reviews = false
	result, err := Analyze(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewActivity.Coverage != "partial" || result.ReviewActivity.ReviewUnits != 4 || result.FirstResponse.CoveredPRs != 4 || result.FirstResponse.MedianMS.Value != nil {
		t.Fatalf("incorrect partial coverage: %+v", result)
	}
}
func TestHistoricalOldPRAndPairDedup(t *testing.T) {
	input := fixture(t)
	input.PullRequests[0].CreatedAt = "2025-12-01T00:00:00Z"
	old := input.PullRequests[0].Reviews[0]
	old.ID = "earlier"
	old.OccurredAt = "2025-12-31T23:59:59Z"
	input.PullRequests[0].Reviews = append(input.PullRequests[0].Reviews, old)
	result, err := Analyze(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreationCohort.EligiblePRs != 4 || result.ReviewActivity.ReviewUnits != 4 {
		t.Fatalf("incorrect population: %+v", result)
	}
}
func TestInvalidInput(t *testing.T) {
	for _, mutate := range []func(*Input){
		func(i *Input) { i.Window.Start = "2026-02-30T00:00:00Z" },
		func(i *Input) { i.Window.Start = i.Window.End },
		func(i *Input) { i.PullRequests = append(i.PullRequests, i.PullRequests[0]) },
		func(i *Input) { i.PullRequests[0].Reviews[0].Actor.ID = 0 },
	} {
		input := fixture(t)
		mutate(&input)
		if _, err := Analyze(input); err == nil {
			t.Fatal("expected invalid input to fail")
		}
	}
}
