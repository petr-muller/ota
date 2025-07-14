package main

import (
	"testing"

	"github.com/petr-muller/ota/internal/mappings"
)

func TestDetermineProject(t *testing.T) {
	tests := []struct {
		name            string
		componentName   string
		providedProject string
		mappings        map[string]string
		expectedProject string
		expectError     bool
	}{
		{
			name:            "no component, no provided project",
			componentName:   "",
			providedProject: "",
			mappings:        map[string]string{},
			expectedProject: "",
			expectError:     true,
		},
		{
			name:            "no component, with provided project",
			componentName:   "",
			providedProject: "TEST",
			mappings:        map[string]string{},
			expectedProject: "TEST",
			expectError:     false,
		},
		{
			name:            "component with no mapping, no provided project",
			componentName:   "test-component",
			providedProject: "",
			mappings:        map[string]string{},
			expectedProject: "",
			expectError:     true,
		},
		{
			name:            "component with mapping, no provided project",
			componentName:   "test-component",
			providedProject: "",
			mappings:        map[string]string{"test-component": "MAPPED"},
			expectedProject: "MAPPED",
			expectError:     false,
		},
		{
			name:            "component with no mapping, with provided project",
			componentName:   "test-component",
			providedProject: "PROVIDED",
			mappings:        map[string]string{},
			expectedProject: "PROVIDED",
			expectError:     false,
		},
		{
			name:            "component with matching mapping and provided project",
			componentName:   "test-component",
			providedProject: "SAME",
			mappings:        map[string]string{"test-component": "SAME"},
			expectedProject: "SAME",
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mappings.NewMappings()
			for comp, proj := range tt.mappings {
				m.SetComponentMapping(comp, proj)
			}

			result, err := determineProject(tt.componentName, tt.providedProject, m)
			
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expectedProject {
				t.Errorf("expected project %q, got %q", tt.expectedProject, result)
			}
		})
	}
}

func TestDetermineTaskType(t *testing.T) {
	tests := []struct {
		name             string
		project          string
		providedTaskType string
		mappings         map[string]string
		expectedTaskType string
	}{
		{
			name:             "no mapping, default task type",
			project:          "TEST",
			providedTaskType: "Spike", // default
			mappings:         map[string]string{},
			expectedTaskType: "Spike",
		},
		{
			name:             "no mapping, non-default task type",
			project:          "TEST",
			providedTaskType: "Task",
			mappings:         map[string]string{},
			expectedTaskType: "Task",
		},
		{
			name:             "mapping exists, default provided, use mapped",
			project:          "TEST",
			providedTaskType: "Spike", // default
			mappings:         map[string]string{"TEST": "Task"},
			expectedTaskType: "Task",
		},
		{
			name:             "mapping exists, non-default provided, use provided",
			project:          "TEST",
			providedTaskType: "Task",
			mappings:         map[string]string{"TEST": "Spike"},
			expectedTaskType: "Task",
		},
		{
			name:             "mapping matches provided",
			project:          "TEST",
			providedTaskType: "Task",
			mappings:         map[string]string{"TEST": "Task"},
			expectedTaskType: "Task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mappings.NewMappings()
			for proj, taskType := range tt.mappings {
				m.SetTaskTypeMapping(proj, taskType)
			}

			result := determineTaskType(tt.project, tt.providedTaskType, m)
			
			if result != tt.expectedTaskType {
				t.Errorf("expected task type %q, got %q", tt.expectedTaskType, result)
			}
		})
	}
}

func TestSaveTaskTypeMappingIfNeeded(t *testing.T) {
	tests := []struct {
		name             string
		project          string
		finalTaskType    string
		existingMappings map[string]string
		expectSave       bool
	}{
		{
			name:             "default task type, should not save",
			project:          "TEST",
			finalTaskType:    "Spike", // default
			existingMappings: map[string]string{},
			expectSave:       false,
		},
		{
			name:             "non-default task type, no existing mapping, should save",
			project:          "TEST",
			finalTaskType:    "Task",
			existingMappings: map[string]string{},
			expectSave:       true,
		},
		{
			name:             "non-default task type, existing mapping, should not save",
			project:          "TEST",
			finalTaskType:    "Task",
			existingMappings: map[string]string{"TEST": "Spike"},
			expectSave:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mappings.NewMappings()
			for proj, taskType := range tt.existingMappings {
				m.SetTaskTypeMapping(proj, taskType)
			}

			initialMapping := m.GetTaskTypeForProject(tt.project)

			saveTaskTypeMappingIfNeeded(tt.project, tt.finalTaskType, m)
			
			result := m.GetTaskTypeForProject(tt.project)
			if tt.expectSave {
				if result != tt.finalTaskType {
					t.Errorf("expected mapping to be saved: %q -> %q, but got %q", tt.project, tt.finalTaskType, result)
				}
			} else {
				if result != initialMapping {
					t.Errorf("expected mapping to remain unchanged at %q, but got %q", initialMapping, result)
				}
			}
		})
	}
}

func TestSaveComponentMappingIfNeeded(t *testing.T) {
	tests := []struct {
		name             string
		componentName    string
		providedProject  string
		finalProject     string
		existingMappings map[string]string
		expectNoChange   bool
	}{
		{
			name:             "empty component name, should not save",
			componentName:    "",
			providedProject:  "TEST",
			finalProject:     "TEST", 
			existingMappings: map[string]string{},
			expectNoChange:   true,
		},
		{
			name:             "empty provided project, should not save",
			componentName:    "test-comp",
			providedProject:  "",
			finalProject:     "TEST",
			existingMappings: map[string]string{},
			expectNoChange:   true,
		},
		{
			name:             "new mapping case",
			componentName:    "test-comp",
			providedProject:  "TEST",
			finalProject:     "TEST",
			existingMappings: map[string]string{},
			expectNoChange:   false,
		},
		{
			name:             "unchanged mapping case", 
			componentName:    "test-comp",
			providedProject:  "TEST",
			finalProject:     "TEST",
			existingMappings: map[string]string{"test-comp": "TEST"},
			expectNoChange:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mappings.NewMappings()
			for comp, proj := range tt.existingMappings {
				m.SetComponentMapping(comp, proj)
			}

			initialMapping := m.GetProjectForComponent(tt.componentName)
			
			saveComponentMappingIfNeeded(tt.componentName, tt.providedProject, tt.finalProject, m)
			
			result := m.GetProjectForComponent(tt.componentName)
			
			if tt.expectNoChange {
				if result != initialMapping {
					t.Errorf("expected no change in mapping, initial: %q, final: %q", initialMapping, result)
				}
			} else {
				if result != tt.finalProject {
					t.Errorf("expected mapping %q -> %q, got %q", tt.componentName, tt.finalProject, result)
				}
			}
		})
	}
}