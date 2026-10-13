package main

import (
	"strings"

	"github.com/cpanato/oss-maintainer-insights/internal/snapshot"
)

type collectionQueue struct {
	Version        int    `json:"version"`
	NextRepository string `json:"next_repository"`
}

// Rotate the first project each run, including interrupted runs, so a shared
// rate limit cannot permanently starve projects later in the catalog.
func rotateCatalog(entries []snapshot.Entry, queue collectionQueue) ([]snapshot.Entry, collectionQueue) {
	if len(entries) == 0 {
		return entries, collectionQueue{Version: 1}
	}
	start := 0
	if queue.Version == 1 {
		for index, entry := range entries {
			if strings.EqualFold(entry.Repository, queue.NextRepository) {
				start = index
				break
			}
		}
	}
	result := append(append([]snapshot.Entry{}, entries[start:]...), entries[:start]...)
	return result, collectionQueue{Version: 1, NextRepository: result[1%len(result)].Repository}
}
