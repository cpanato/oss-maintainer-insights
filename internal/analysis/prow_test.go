package analysis

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSharedProwParser(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/prow-commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Body string
		Expected   []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			if got := ParseReviewCommands(test.Body); !reflect.DeepEqual(got, test.Expected) {
				t.Fatalf("commands %v, expected %v", got, test.Expected)
			}
		})
	}
}
func TestSharedProwParticipation(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/prow-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input Input
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	result, err := Analyze(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewActivity.ReviewUnits != 8 || result.ReviewActivity.Participants != 5 || result.ReviewActivity.Sources.GitHub != 5 || result.ReviewActivity.Sources.Prow != 4 {
		t.Fatalf("incorrect deduplication/origins: %+v", result.ReviewActivity)
	}
	for _, row := range result.ReviewActivity.Distribution {
		if row.Actor.ID == 50 || row.Actor.ID == 60 || row.Actor.ID == 70 || row.Actor.ID == 90 || row.Actor.ID == 104 {
			t.Fatal("cancelled, excluded, or future signal counted")
		}
	}
	input.PullRequests[0].Coverage.IssueComments = false
	partial, err := Analyze(input)
	if err != nil {
		t.Fatal(err)
	}
	if partial.ReviewActivity.CoveredPRs != 4 || partial.ReviewActivity.ReviewUnits != 6 {
		t.Fatal("incomplete comment stream contributed to review participation")
	}
}
