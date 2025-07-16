package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Store handles persistent storage of query information
type Store struct {
	dataDir string
}

// NewStore creates a new storage instance
func NewStore(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
	}
}

// ensureDataDir creates the data directory if it doesn't exist
func (s *Store) ensureDataDir() error {
	return os.MkdirAll(s.dataDir, 0755)
}

// queryFilePath returns the file path for a given query name
func (s *Store) queryFilePath(name string) string {
	return filepath.Join(s.dataDir, fmt.Sprintf("%s.yaml", name))
}

// SaveQuery saves a query to the storage
func (s *Store) SaveQuery(query QueryInfo) error {
	if err := s.ensureDataDir(); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	filePath := s.queryFilePath(query.Name)
	
	data, err := yaml.Marshal(query)
	if err != nil {
		return fmt.Errorf("failed to marshal query: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write query file: %w", err)
	}

	return nil
}

// LoadQuery loads a query from storage
func (s *Store) LoadQuery(name string) (*QueryInfo, error) {
	filePath := s.queryFilePath(name)
	
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Query doesn't exist
		}
		return nil, fmt.Errorf("failed to read query file: %w", err)
	}

	var query QueryInfo
	if err := yaml.Unmarshal(data, &query); err != nil {
		return nil, fmt.Errorf("failed to unmarshal query: %w", err)
	}

	return &query, nil
}

// QueryExists checks if a query exists in storage
func (s *Store) QueryExists(name string) bool {
	filePath := s.queryFilePath(name)
	_, err := os.Stat(filePath)
	return err == nil
}

// ListQueries returns all stored query names
func (s *Store) ListQueries() ([]string, error) {
	if err := s.ensureDataDir(); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read data directory: %w", err)
	}

	var queries []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			// Remove .yaml extension
			name := strings.TrimSuffix(entry.Name(), ".yaml")
			queries = append(queries, name)
		}
	}

	return queries, nil
}

// ListQueriesDetailed returns all stored queries with their details
func (s *Store) ListQueriesDetailed() ([]QueryListItem, error) {
	if err := s.ensureDataDir(); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read data directory: %w", err)
	}

	var queries []QueryListItem
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			// Remove .yaml extension
			name := strings.TrimSuffix(entry.Name(), ".yaml")
			
			// Load the query to get details
			query, err := s.LoadQuery(name)
			if err != nil {
				continue // Skip queries that can't be loaded
			}
			
			item := QueryListItem{
				Name:        query.Name,
				Description: query.Description,
				JQL:         query.JQL,
				LastFetched: query.LastFetched,
				IssueCount:  len(query.Issues),
			}
			queries = append(queries, item)
		}
	}

	return queries, nil
}

// DeleteQuery removes a query from storage
func (s *Store) DeleteQuery(name string) error {
	filePath := s.queryFilePath(name)
	
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete query file: %w", err)
	}

	return nil
}

// GetDataDir returns the data directory path
func (s *Store) GetDataDir() string {
	return s.dataDir
}