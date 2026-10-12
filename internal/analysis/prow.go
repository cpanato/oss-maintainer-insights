package analysis

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var hiddenCommandText = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)
var commandFence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
var commandLine = regexp.MustCompile(`(?i)^ {0,3}/(lgtm|approve)(?:[ \t]+(cancel|no-issue))?[ \t]*$`)
var removeCommandLine = regexp.MustCompile(`(?i)^ {0,3}/remove-(lgtm|approve)[ \t]*$`)

// ParseReviewCommands extracts at most four normalized signals, never source text.
func ParseReviewCommands(body string) []string {
	type signal struct{ active, cancelled bool }
	state := map[string]signal{}
	fence := ""
	length := 0
	for _, line := range strings.Split(hiddenCommandText.ReplaceAllString(body, ""), "\n") {
		line = strings.TrimSuffix(line, "\r")
		marker := commandFence.FindStringSubmatch(line)
		if fence != "" {
			if marker != nil && marker[1][:1] == fence && len(marker[1]) >= length && strings.TrimSpace(marker[2]) == "" {
				fence = ""
			}
			continue
		}
		if marker != nil {
			fence = marker[1][:1]
			length = len(marker[1])
			continue
		}
		match := commandLine.FindStringSubmatch(line)
		remove := removeCommandLine.FindStringSubmatch(line)
		if match == nil && remove == nil {
			continue
		}
		kind := ""
		cancelled := remove != nil
		if remove != nil {
			kind = strings.ToLower(remove[1])
		} else {
			kind = strings.ToLower(match[1])
			cancelled = strings.EqualFold(match[2], "cancel")
		}
		previous := state[kind]
		state[kind] = signal{active: !cancelled, cancelled: cancelled || previous.cancelled}
	}
	commands := []string{}
	for _, kind := range []string{"lgtm", "approve"} {
		current, exists := state[kind]
		if !exists {
			continue
		}
		if current.cancelled {
			commands = append(commands, kind+"_cancel")
		}
		if current.active {
			commands = append(commands, kind)
		}
	}
	return commands
}

type reviewPair struct {
	event        Event
	github, prow bool
}

func reviewPairs(reviews []Review, comments []Event, created, observed time.Time) map[int64]reviewPair {
	pairs := map[int64]reviewPair{}
	within := func(e Event) bool { at, _ := utc(e.OccurredAt); return !at.Before(created) && !at.After(observed) }
	include := func(e Event, github bool) {
		pair, exists := pairs[e.Actor.ID]
		at, _ := utc(e.OccurredAt)
		previous, _ := utc(pair.event.OccurredAt)
		if !exists || at.Before(previous) || (at.Equal(previous) && eventIDBefore(e.ID, pair.event.ID)) {
			pair.event = e
		}
		if github {
			pair.github = true
		} else {
			pair.prow = true
		}
		pairs[e.Actor.ID] = pair
	}
	for _, r := range reviews {
		if within(r.Event) {
			include(r.Event, true)
		}
	}
	ordered := []Event{}
	for _, e := range comments {
		if within(e) {
			ordered = append(ordered, e)
		}
	}
	for _, r := range reviews {
		if within(r.Event) {
			ordered = append(ordered, r.Event)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		first, _ := utc(ordered[i].OccurredAt)
		second, _ := utc(ordered[j].OccurredAt)
		if !first.Equal(second) {
			return first.Before(second)
		}
		return eventIDBefore(ordered[i].ID, ordered[j].ID)
	})
	signals := map[int64]map[string]Event{}
	for _, e := range ordered {
		actor := signals[e.Actor.ID]
		if actor == nil {
			actor = map[string]Event{}
		}
		for _, command := range e.ReviewCommands {
			kind := "approve"
			if strings.HasPrefix(command, "lgtm") {
				kind = "lgtm"
			}
			if strings.HasSuffix(command, "_cancel") {
				delete(actor, kind)
			} else if _, exists := actor[kind]; !exists {
				actor[kind] = e
			}
		}
		signals[e.Actor.ID] = actor
	}
	for _, actor := range signals {
		for _, e := range actor {
			include(e, false)
		}
	}
	return pairs
}

func eventIDBefore(a, b string) bool {
	first, ea := strconv.ParseUint(a, 10, 64)
	second, eb := strconv.ParseUint(b, 10, 64)
	if ea == nil && eb == nil {
		return first < second
	}
	return a < b
}
