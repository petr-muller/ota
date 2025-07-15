package storage

import (
	"time"
)

// QueryInfo represents metadata about a stored query
type QueryInfo struct {
	Name         string    `yaml:"name"`
	JQL          string    `yaml:"jql"`
	LastFetched  time.Time `yaml:"last_fetched"`
	Issues       []Issue   `yaml:"issues"`
}

// Issue represents a JIRA issue with the fields we care about
type Issue struct {
	Key         string    `yaml:"key"`
	Summary     string    `yaml:"summary"`
	Component   string    `yaml:"component"`
	Status      string    `yaml:"status"`
	LastUpdated time.Time `yaml:"last_updated"`
	Labels      []string  `yaml:"labels"`
	Assignee    string    `yaml:"assignee"`
}

// IssueChange represents a change in an issue field
type IssueChange struct {
	Field    string `yaml:"field"`
	OldValue string `yaml:"old_value"`
	NewValue string `yaml:"new_value"`
}

// QueryResult represents the result of running a query with change tracking
type QueryResult struct {
	Query        QueryInfo              `yaml:"query"`
	NewIssues    []Issue               `yaml:"new_issues"`
	RemovedIssues []Issue              `yaml:"removed_issues"`
	ChangedIssues map[string][]IssueChange `yaml:"changed_issues"`
}