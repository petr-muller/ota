package service

import (
	"context"
	"fmt"
	"time"

	"github.com/petr-muller/ota/internal/flagutil"
	"github.com/petr-muller/ota/internal/jirawatch/compare"
	"github.com/petr-muller/ota/internal/jirawatch/jira"
	"github.com/petr-muller/ota/internal/jirawatch/storage"
)

// Service orchestrates the jira-query-watch functionality
type Service struct {
	jiraClient *jira.Client
	store      *storage.Store
}

// NewService creates a new service instance
func NewService(jiraOptions flagutil.JiraOptions, dataDir string) (*Service, error) {
	jiraClient, err := jira.NewClient(jiraOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create JIRA client: %w", err)
	}

	store := storage.NewStore(dataDir)

	return &Service{
		jiraClient: jiraClient,
		store:      store,
	}, nil
}

// WatchQueryOptions contains options for watching a query
type WatchQueryOptions struct {
	Name string
	JQL  string
}

// WatchQuery executes a query and compares results with stored data
func (s *Service) WatchQuery(ctx context.Context, opts WatchQueryOptions) (*storage.QueryResult, error) {
	// Validate JQL
	if err := s.jiraClient.ValidateJQL(ctx, opts.JQL); err != nil {
		return nil, fmt.Errorf("invalid JQL: %w", err)
	}

	// Load existing query if it exists
	existingQuery, err := s.store.LoadQuery(opts.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing query: %w", err)
	}

	// Fetch current issues
	currentIssues, err := s.jiraClient.ExecuteQuery(ctx, opts.JQL)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	// Compare with previous results
	var previousIssues []storage.Issue
	var lastFetched time.Time
	
	if existingQuery != nil {
		previousIssues = existingQuery.Issues
		lastFetched = existingQuery.LastFetched
	}

	result := compare.CompareQueries(currentIssues, previousIssues)

	// Update the query info
	queryInfo := storage.QueryInfo{
		Name:        opts.Name,
		JQL:         opts.JQL,
		LastFetched: time.Now(),
		Issues:      currentIssues,
	}

	// Store the updated query
	if err := s.store.SaveQuery(queryInfo); err != nil {
		return nil, fmt.Errorf("failed to save query: %w", err)
	}

	// Set the query and last fetched time in the result
	result.Query = queryInfo
	result.Query.LastFetched = lastFetched // Use the previous fetch time for display

	return &result, nil
}

// InspectQuery loads and displays the current state of a stored query
func (s *Service) InspectQuery(ctx context.Context, name string) (*storage.QueryResult, error) {
	// Load existing query
	existingQuery, err := s.store.LoadQuery(name)
	if err != nil {
		return nil, fmt.Errorf("failed to load query: %w", err)
	}

	if existingQuery == nil {
		return nil, fmt.Errorf("query '%s' not found", name)
	}

	// Re-execute the query to get current state
	currentIssues, err := s.jiraClient.ExecuteQuery(ctx, existingQuery.JQL)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	// Compare with stored results
	result := compare.CompareQueries(currentIssues, existingQuery.Issues)

	// Update the query info
	queryInfo := storage.QueryInfo{
		Name:        existingQuery.Name,
		JQL:         existingQuery.JQL,
		LastFetched: time.Now(),
		Issues:      currentIssues,
	}

	// Store the updated query
	if err := s.store.SaveQuery(queryInfo); err != nil {
		return nil, fmt.Errorf("failed to save query: %w", err)
	}

	// Set the query and last fetched time in the result
	result.Query = queryInfo
	result.Query.LastFetched = existingQuery.LastFetched // Use the previous fetch time for display

	return &result, nil
}

// ListQueries returns all stored query names
func (s *Service) ListQueries() ([]string, error) {
	return s.store.ListQueries()
}

// DeleteQuery removes a stored query
func (s *Service) DeleteQuery(name string) error {
	return s.store.DeleteQuery(name)
}

// QueryExists checks if a query exists in storage
func (s *Service) QueryExists(name string) bool {
	return s.store.QueryExists(name)
}