package mappings

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/petr-muller/ota/internal/config"
)

const (
	mappingsFileName = "mappings.yaml"
)

// Mappings holds the component and project mappings
type Mappings struct {
	// ComponentToProject maps Jira component names to project keys
	ComponentToProject map[string]string `yaml:"componentToProject"`
	// ProjectToTaskType maps project keys to task types
	ProjectToTaskType map[string]string `yaml:"projectToTaskType"`
}

// NewMappings creates a new empty mappings structure
func NewMappings() *Mappings {
	return &Mappings{
		ComponentToProject: make(map[string]string),
		ProjectToTaskType:  make(map[string]string),
	}
}

// LoadMappings loads mappings from the default location, returns empty mappings if file doesn't exist
func LoadMappings() (*Mappings, error) {
	mappingsPath := filepath.Join(config.MustOtaConfigDir(), mappingsFileName)
	
	// If file doesn't exist, return empty mappings
	if _, err := os.Stat(mappingsPath); os.IsNotExist(err) {
		return NewMappings(), nil
	}

	data, err := os.ReadFile(mappingsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read mappings file: %w", err)
	}

	var mappings Mappings
	if err := yaml.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("failed to parse mappings file: %w", err)
	}

	// Initialize maps if they're nil
	if mappings.ComponentToProject == nil {
		mappings.ComponentToProject = make(map[string]string)
	}
	if mappings.ProjectToTaskType == nil {
		mappings.ProjectToTaskType = make(map[string]string)
	}

	return &mappings, nil
}

// SaveMappings saves mappings to the default location
func (m *Mappings) SaveMappings() error {
	otaConfigDir := config.MustOtaConfigDir()
	
	// Ensure config directory exists
	if err := os.MkdirAll(otaConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	mappingsPath := filepath.Join(otaConfigDir, mappingsFileName)
	
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal mappings: %w", err)
	}

	if err := os.WriteFile(mappingsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write mappings file: %w", err)
	}

	return nil
}

// GetProjectForComponent returns the mapped project for a component, empty string if not found
func (m *Mappings) GetProjectForComponent(component string) string {
	return m.ComponentToProject[component]
}

// GetTaskTypeForProject returns the mapped task type for a project, empty string if not found
func (m *Mappings) GetTaskTypeForProject(project string) string {
	return m.ProjectToTaskType[project]
}

// SetComponentMapping sets a component to project mapping
func (m *Mappings) SetComponentMapping(component, project string) {
	m.ComponentToProject[component] = project
}

// SetTaskTypeMapping sets a project to task type mapping
func (m *Mappings) SetTaskTypeMapping(project, taskType string) {
	m.ProjectToTaskType[project] = taskType
}