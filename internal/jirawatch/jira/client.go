package jira

import (
	"context"
	"fmt"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/petr-muller/ota/internal/flagutil"
	"github.com/petr-muller/ota/internal/jirawatch/storage"
	prowjira "sigs.k8s.io/prow/pkg/jira"
)

// Client wraps the prow jira client with our specific functionality
type Client struct {
	jiraClient prowjira.Client
}

// NewClient creates a new JIRA client using the existing flagutil pattern
func NewClient(jiraOptions flagutil.JiraOptions) (*Client, error) {
	jiraClient, err := jiraOptions.Client()
	if err != nil {
		return nil, fmt.Errorf("failed to create JIRA client: %w", err)
	}

	return &Client{
		jiraClient: jiraClient,
	}, nil
}

// ExecuteQuery executes a JQL query and returns the matching issues
func (c *Client) ExecuteQuery(ctx context.Context, jql string) ([]storage.Issue, error) {
	issues, _, err := c.jiraClient.SearchWithContext(ctx, jql, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute JQL query: %w", err)
	}

	var result []storage.Issue
	for _, issue := range issues {
		storageIssue, err := c.convertIssue(issue)
		if err != nil {
			return nil, fmt.Errorf("failed to convert issue %s: %w", issue.Key, err)
		}
		result = append(result, storageIssue)
	}

	return result, nil
}

// convertIssue converts a go-jira Issue to our storage Issue
func (c *Client) convertIssue(issue jira.Issue) (storage.Issue, error) {
	// Extract component (take first one if multiple)
	component := ""
	if len(issue.Fields.Components) > 0 {
		component = issue.Fields.Components[0].Name
	}

	// Extract status
	status := ""
	if issue.Fields.Status != nil {
		status = issue.Fields.Status.Name
	}

	// Extract assignee
	assignee := ""
	if issue.Fields.Assignee != nil {
		assignee = issue.Fields.Assignee.DisplayName
	}

	// Extract labels
	labels := make([]string, len(issue.Fields.Labels))
	copy(labels, issue.Fields.Labels)

	// Convert last updated time
	lastUpdated := time.Time(issue.Fields.Updated)

	return storage.Issue{
		Key:         issue.Key,
		Summary:     issue.Fields.Summary,
		Component:   component,
		Status:      status,
		LastUpdated: lastUpdated,
		Labels:      labels,
		Assignee:    assignee,
	}, nil
}

// ValidateJQL validates a JQL query by attempting to execute it with a limit of 1
func (c *Client) ValidateJQL(ctx context.Context, jql string) error {
	options := &jira.SearchOptions{
		MaxResults: 1,
	}
	
	_, _, err := c.jiraClient.SearchWithContext(ctx, jql, options)
	if err != nil {
		return fmt.Errorf("invalid JQL query: %w", err)
	}

	return nil
}