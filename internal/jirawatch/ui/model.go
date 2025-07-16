package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petr-muller/ota/internal/jirawatch/storage"
)

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// formatDuration formats a duration into a human-readable string
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	} else {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd", days)
	}
}

// Model represents the TUI model for displaying query results
type Model struct {
	table          table.Model
	queryResult    storage.QueryResult
	queryName      string
	lastFetched    time.Time
	width          int
	height         int
	displayedIssues []storage.Issue // Issues as they appear in the table
}

// NewModel creates a new TUI model
func NewModel(queryName string, queryResult storage.QueryResult, lastFetched time.Time) Model {
	// Start with default columns - they will be resized when terminal size is known
	// Summary will be displayed on a separate line, not as a column
	columns := []table.Column{
		{Title: "Key", Width: 10},
		{Title: "Component", Width: 12},
		{Title: "Status", Width: 8},
		{Title: "Last Updated", Width: 10},
		{Title: "Labels", Width: 15},
		{Title: "Assignee", Width: 12},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(1), // Will be dynamically adjusted based on content
	)

	// Customize table styles for better selection visibility
	s := table.DefaultStyles()
	
	// Set default selection style (will be overridden dynamically)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("230")).  // Light yellow text
		Background(lipgloss.Color("240")).  // Default grey background
		Bold(true)
	
	// Try to disable table's own width management
	s.Cell = s.Cell.MaxWidth(0) // Disable max width
	s.Header = s.Header.MaxWidth(0) // Disable max width for headers
	
	t.SetStyles(s)

	m := Model{
		table:       t,
		queryResult: queryResult,
		queryName:   queryName,
		lastFetched: lastFetched,
	}

	m.updateTable()
	m.updateSelectionStyle()
	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateTableSize()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	m.table, cmd = m.table.Update(msg)
	
	// Update selection style based on selected item status
	m.updateSelectionStyle()
	
	return m, cmd
}

// View renders the model
func (m Model) View() string {
	var s strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)

	s.WriteString(headerStyle.Render(fmt.Sprintf("Query: %s", m.queryName)))
	s.WriteString("\n")
	
	// Description if available
	if m.queryResult.Query.Description != "" {
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Italic(true)
		s.WriteString(descStyle.Render(m.queryResult.Query.Description))
		s.WriteString("\n")
	}

	// Last fetched info
	if !m.lastFetched.IsZero() {
		infoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
		duration := time.Since(m.lastFetched)
		s.WriteString(infoStyle.Render(fmt.Sprintf("Changes since: %s (%s ago)", 
			m.lastFetched.Format("2006-01-02 15:04:05"), 
			formatDuration(duration))))
		s.WriteString("\n")
	}

	// Summary of changes
	if len(m.queryResult.NewIssues) > 0 || len(m.queryResult.RemovedIssues) > 0 || len(m.queryResult.ChangedIssues) > 0 {
		summaryStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("33")).
			MarginTop(1).
			MarginBottom(1)

		summary := fmt.Sprintf("Changes: %d new, %d changed, %d removed",
			len(m.queryResult.NewIssues),
			len(m.queryResult.ChangedIssues),
			len(m.queryResult.RemovedIssues))

		s.WriteString(summaryStyle.Render(summary))
		s.WriteString("\n")
	}

	// Table
	s.WriteString(m.table.View())
	s.WriteString("\n")

	// Show scroll indicator if there are more items than fit in the table
	if len(m.displayedIssues) > 15 {
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)
		s.WriteString(scrollStyle.Render(fmt.Sprintf("Showing 15 of %d items - use arrow keys to scroll", len(m.displayedIssues))))
		s.WriteString("\n")
	}

	// Show summary of the selected issue
	if len(m.displayedIssues) > 0 {
		cursor := m.table.Cursor()
		if cursor >= 0 && cursor < len(m.displayedIssues) {
			selectedIssue := m.displayedIssues[cursor]
			summaryStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("250")).
				MarginTop(1)
			s.WriteString(summaryStyle.Render(fmt.Sprintf("Summary: %s", selectedIssue.Summary)))
			s.WriteString("\n")
		}
	}

	// Item status panel (right below the table)
	statusPanel := m.renderItemStatus()
	if statusPanel != "" {
		s.WriteString(statusPanel)
	}

	// Help
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginTop(1)
	s.WriteString(helpStyle.Render("Press 'q' to quit, arrow keys to navigate"))

	return s.String()
}

// updateTable updates the table with current data
func (m *Model) updateTable() {
	var rows []table.Row
	m.displayedIssues = []storage.Issue{} // Reset displayed issues

	// Combine all issues and sort by last updated (desc)
	allIssues := append([]storage.Issue{}, m.queryResult.Query.Issues...)
	sort.Slice(allIssues, func(i, j int) bool {
		return allIssues[i].LastUpdated.After(allIssues[j].LastUpdated)
	})

	// Store displayed issues first for width calculation
	for _, issue := range allIssues {
		m.displayedIssues = append(m.displayedIssues, issue)
	}
	for _, issue := range m.queryResult.RemovedIssues {
		m.displayedIssues = append(m.displayedIssues, issue)
	}

	// Calculate column widths based on raw content before styling
	m.updateColumnWidths()

	// Create unstyled rows - let the table handle selection styling
	for _, issue := range allIssues {
		row := m.issueToRow(issue, lipgloss.NewStyle())
		rows = append(rows, row)
	}

	// Add removed issues at the bottom
	for _, issue := range m.queryResult.RemovedIssues {
		row := m.issueToRow(issue, lipgloss.NewStyle())
		rows = append(rows, row)
	}

	m.table.SetRows(rows)
	
	// Update table size based on new content
	m.updateTableSize()
	
	// Update selection style for the new data
	m.updateSelectionStyle()
}

// issueToRow converts an issue to a table row with styling
func (m *Model) issueToRow(issue storage.Issue, style lipgloss.Style) table.Row {
	lastUpdated := issue.LastUpdated.Format("2006-01-02")
	labels := strings.Join(issue.Labels, ", ")

	return table.Row{
		style.Render(issue.Key),
		style.Render(issue.Component),
		style.Render(issue.Status),
		style.Render(lastUpdated),
		style.Render(labels),
		style.Render(issue.Assignee),
	}
}

// getIssueStyle returns the appropriate style for an issue
func (m *Model) getIssueStyle(issue storage.Issue) lipgloss.Style {
	// Check if it's a new issue
	for _, newIssue := range m.queryResult.NewIssues {
		if newIssue.Key == issue.Key {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("46")) // Bright green
		}
	}

	// Check if it's a changed issue
	if _, hasChanges := m.queryResult.ChangedIssues[issue.Key]; hasChanges {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")) // Bright yellow
	}

	// Default style
	return lipgloss.NewStyle()
}

// getRemovedStyle returns the style for removed issues
func (m *Model) getRemovedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Strikethrough(true)
}

// updateTableSize updates the table size based on terminal dimensions
func (m *Model) updateTableSize() {
	if m.width > 0 && m.height > 0 {
		// Set table height based on content, but limit to 15 rows max
		// Add 1 for the header row
		tableHeight := min(len(m.displayedIssues), 15) + 1
		if tableHeight < 2 {
			tableHeight = 2  // At least header + 1 row
		}
		m.table.SetHeight(tableHeight)
		
		// Recalculate column widths based on terminal width
		m.updateColumnWidths()
	}
}

// updateColumnWidths recalculates column widths based on terminal width and data
func (m *Model) updateColumnWidths() {
	if m.width <= 0 {
		return
	}
	
	// Reserve space for table borders and padding
	availableWidth := m.width - 10
	
	// Calculate optimal widths based on actual data
	dataWidths := m.calculateDataWidths()
	
	// Add some padding to data widths
	for key, width := range dataWidths {
		dataWidths[key] = width + 4 // Add 4 chars padding for styled content
	}
	
	// Calculate total data width needed
	totalDataWidth := 0
	for _, width := range dataWidths {
		totalDataWidth += width
	}
	
	// If we have more space than needed, distribute it proportionally
	var columns []table.Column
	if availableWidth > totalDataWidth {
		extraWidth := availableWidth - totalDataWidth
		
		// Define how much extra space each column should get (as a proportion)
		extraDistribution := map[string]float64{
			"Key":          0.05,  // 5% of extra space
			"Component":    0.25,  // 25% of extra space
			"Status":       0.05,  // 5% of extra space
			"Last Updated": 0.05,  // 5% of extra space
			"Labels":       0.45,  // 45% of extra space (labels tend to be long)
			"Assignee":     0.15,  // 15% of extra space
		}
		
		columns = []table.Column{
			{Title: "Key", Width: dataWidths["Key"] + int(float64(extraWidth)*extraDistribution["Key"])},
			{Title: "Component", Width: dataWidths["Component"] + int(float64(extraWidth)*extraDistribution["Component"])},
			{Title: "Status", Width: dataWidths["Status"] + int(float64(extraWidth)*extraDistribution["Status"])},
			{Title: "Last Updated", Width: dataWidths["Last Updated"] + int(float64(extraWidth)*extraDistribution["Last Updated"])},
			{Title: "Labels", Width: dataWidths["Labels"] + int(float64(extraWidth)*extraDistribution["Labels"])},
			{Title: "Assignee", Width: dataWidths["Assignee"] + int(float64(extraWidth)*extraDistribution["Assignee"])},
		}
	} else {
		// Use data widths if terminal is too narrow for extra space
		columns = []table.Column{
			{Title: "Key", Width: dataWidths["Key"]},
			{Title: "Component", Width: dataWidths["Component"]},
			{Title: "Status", Width: dataWidths["Status"]},
			{Title: "Last Updated", Width: dataWidths["Last Updated"]},
			{Title: "Labels", Width: dataWidths["Labels"]},
			{Title: "Assignee", Width: dataWidths["Assignee"]},
		}
	}
	
	m.table.SetColumns(columns)
}

// calculateDataWidths calculates the optimal width for each column based on actual data
func (m *Model) calculateDataWidths() map[string]int {
	widths := map[string]int{
		"Key":          len("Key"),
		"Component":    len("Component"),
		"Status":       len("Status"),
		"Last Updated": len("Last Updated"),
		"Labels":       len("Labels"),
		"Assignee":     len("Assignee"),
	}
	
	// Check all displayed issues
	for _, issue := range m.displayedIssues {
		if len(issue.Key) > widths["Key"] {
			widths["Key"] = len(issue.Key)
		}
		if len(issue.Component) > widths["Component"] {
			widths["Component"] = len(issue.Component)
		}
		if len(issue.Status) > widths["Status"] {
			widths["Status"] = len(issue.Status)
		}
		
		// Last Updated is always in YYYY-MM-DD format
		lastUpdated := issue.LastUpdated.Format("2006-01-02")
		if len(lastUpdated) > widths["Last Updated"] {
			widths["Last Updated"] = len(lastUpdated)
		}
		
		// Labels are joined with ", "
		labels := strings.Join(issue.Labels, ", ")
		if len(labels) > widths["Labels"] {
			widths["Labels"] = len(labels)
		}
		
		if len(issue.Assignee) > widths["Assignee"] {
			widths["Assignee"] = len(issue.Assignee)
		}
	}
	
	return widths
}

// renderItemStatus creates a status panel for the selected item
func (m *Model) renderItemStatus() string {
	if len(m.displayedIssues) == 0 {
		return ""
	}
	
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(m.displayedIssues) {
		return ""
	}
	
	selectedIssue := m.displayedIssues[cursor]
	var s strings.Builder
	
	// Determine item status and show it
	if m.isNewIssue(selectedIssue) {
		newStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true)
		s.WriteString(newStyle.Render("NEW ITEM"))
		s.WriteString("\n")
	} else if m.isChangedIssue(selectedIssue) {
		changedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true)
		s.WriteString(changedStyle.Render("CHANGED ITEM"))
		s.WriteString("\n")
		
		// Show what changed
		changes := m.queryResult.ChangedIssues[selectedIssue.Key]
		for _, change := range changes {
			s.WriteString(fmt.Sprintf("  • %s changed from '%s' to '%s'\n", 
				change.Field, change.OldValue, change.NewValue))
		}
	} else if m.isRemovedIssue(selectedIssue) {
		removedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Strikethrough(true)
		s.WriteString(removedStyle.Render("REMOVED ITEM"))
		s.WriteString("\n")
	} else {
		unchangedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		s.WriteString(unchangedStyle.Render("UNCHANGED ITEM"))
		s.WriteString("\n")
	}
	
	return s.String()
}

// Helper methods to check issue status
func (m *Model) isNewIssue(issue storage.Issue) bool {
	for _, newIssue := range m.queryResult.NewIssues {
		if newIssue.Key == issue.Key {
			return true
		}
	}
	return false
}

func (m *Model) isChangedIssue(issue storage.Issue) bool {
	_, exists := m.queryResult.ChangedIssues[issue.Key]
	return exists
}

func (m *Model) isRemovedIssue(issue storage.Issue) bool {
	for _, removedIssue := range m.queryResult.RemovedIssues {
		if removedIssue.Key == issue.Key {
			return true
		}
	}
	return false
}

// updateSelectionStyle updates the table's selection style based on the selected item's status
func (m *Model) updateSelectionStyle() {
	if len(m.displayedIssues) == 0 {
		return
	}
	
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(m.displayedIssues) {
		return
	}
	
	selectedIssue := m.displayedIssues[cursor]
	styles := table.DefaultStyles()
	
	// Determine background color based on item status
	var backgroundColor lipgloss.Color
	if m.isNewIssue(selectedIssue) {
		backgroundColor = lipgloss.Color("22")  // Dark green
	} else if m.isChangedIssue(selectedIssue) {
		backgroundColor = lipgloss.Color("130") // Dark yellow/orange
	} else if m.isRemovedIssue(selectedIssue) {
		backgroundColor = lipgloss.Color("52")  // Dark red
	} else {
		backgroundColor = lipgloss.Color("240") // Grey (unchanged)
	}
	
	// Update the selection style
	styles.Selected = styles.Selected.
		Foreground(lipgloss.Color("230")).  // Light text for contrast
		Background(backgroundColor).
		Bold(true)
	
	// Try to disable table's own width management
	styles.Cell = styles.Cell.MaxWidth(0) // Disable max width
	styles.Header = styles.Header.MaxWidth(0) // Disable max width for headers
	
	m.table.SetStyles(styles)
}