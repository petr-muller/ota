package compare

import (
	"reflect"
	"strings"
	"time"

	"github.com/petr-muller/ota/internal/jirawatch/storage"
)

// CompareQueries compares current issues with previously stored issues
func CompareQueries(current, previous []storage.Issue) storage.QueryResult {
	// Create maps for efficient lookups
	currentMap := make(map[string]storage.Issue)
	previousMap := make(map[string]storage.Issue)

	for _, issue := range current {
		currentMap[issue.Key] = issue
	}

	for _, issue := range previous {
		previousMap[issue.Key] = issue
	}

	var newIssues []storage.Issue
	var removedIssues []storage.Issue
	changedIssues := make(map[string][]storage.IssueChange)

	// Find new issues (in current but not in previous)
	for key, issue := range currentMap {
		if _, exists := previousMap[key]; !exists {
			newIssues = append(newIssues, issue)
		}
	}

	// Find removed issues (in previous but not in current)
	for key, issue := range previousMap {
		if _, exists := currentMap[key]; !exists {
			removedIssues = append(removedIssues, issue)
		}
	}

	// Find changed issues (in both but with different values)
	for key, currentIssue := range currentMap {
		if previousIssue, exists := previousMap[key]; exists {
			changes := compareIssues(currentIssue, previousIssue)
			if len(changes) > 0 {
				changedIssues[key] = changes
			}
		}
	}

	return storage.QueryResult{
		NewIssues:     newIssues,
		RemovedIssues: removedIssues,
		ChangedIssues: changedIssues,
	}
}

// compareIssues compares two issues and returns a list of changes
func compareIssues(current, previous storage.Issue) []storage.IssueChange {
	var changes []storage.IssueChange

	// Compare summary
	if current.Summary != previous.Summary {
		changes = append(changes, storage.IssueChange{
			Field:    "summary",
			OldValue: previous.Summary,
			NewValue: current.Summary,
		})
	}

	// Compare component
	if current.Component != previous.Component {
		changes = append(changes, storage.IssueChange{
			Field:    "component",
			OldValue: previous.Component,
			NewValue: current.Component,
		})
	}

	// Compare status
	if current.Status != previous.Status {
		changes = append(changes, storage.IssueChange{
			Field:    "status",
			OldValue: previous.Status,
			NewValue: current.Status,
		})
	}

	// Compare assignee
	if current.Assignee != previous.Assignee {
		changes = append(changes, storage.IssueChange{
			Field:    "assignee",
			OldValue: previous.Assignee,
			NewValue: current.Assignee,
		})
	}

	// Compare last updated (only if it's significantly different)
	if !current.LastUpdated.Equal(previous.LastUpdated) {
		changes = append(changes, storage.IssueChange{
			Field:    "last_updated",
			OldValue: previous.LastUpdated.Format(time.RFC3339),
			NewValue: current.LastUpdated.Format(time.RFC3339),
		})
	}

	// Compare labels
	if !reflect.DeepEqual(current.Labels, previous.Labels) {
		changes = append(changes, storage.IssueChange{
			Field:    "labels",
			OldValue: strings.Join(previous.Labels, ", "),
			NewValue: strings.Join(current.Labels, ", "),
		})
	}

	return changes
}

// HasChanges returns true if there are any changes in the query result
func HasChanges(result storage.QueryResult) bool {
	return len(result.NewIssues) > 0 || len(result.RemovedIssues) > 0 || len(result.ChangedIssues) > 0
}
