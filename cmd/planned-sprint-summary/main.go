package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/petr-muller/ota/internal/flagutil"
)

type options struct {
	jira           flagutil.JiraOptions
	filter         string
	output         string
	markdown       string
	previousSprint string
}

func (o *options) validate() error {
	return o.jira.Validate()
}

type CardData struct {
	Key           string `yaml:"key"`
	URL           string `yaml:"url"`
	Title         string `yaml:"title,omitempty"`
	QEInvolvement string `yaml:"qe_involvement,omitempty"`
	TechDomain    string `yaml:"technical_domain,omitempty"`
	Summary       string `yaml:"summary,omitempty"`
	Skipped       bool   `yaml:"skipped,omitempty"`
	Final         bool   `yaml:"final,omitempty"`

	// Non-exported field to track if this card was prefilled from existing YAML
	prefilled bool
}

type SprintSummary struct {
	Cards []CardData `yaml:"cards"`
}

type sprintComparison struct {
	completed []CardData
	carryover []CardData
	new       []CardData
}

type jiraClient interface {
	SearchWithContext(context.Context, string, *jira.SearchOptions) ([]jira.Issue, *jira.Response, error)
	JiraURL() string
}

type step int

const (
	stepLoading step = iota
	stepComparison
	stepQEInvolvement
	stepTechDomain
	stepSummary
	stepComplete
)

var (
	qeOptions = []string{
		"Needs QE involvement",
		"Needs QE awareness",
		"OSUS Operations",
		"QE involvement not needed",
	}

	defaultTechDomains = []string{
		"CVO",
		"CLI",
		"OSUS",
		"OSUS Operator",
		"CI",
	}
)

type model struct {
	jira               jiraClient
	filterName         string
	outputFile         string
	markdownFile       string
	previousSprintFile string

	cards       []jira.Issue
	currentCard int
	currentStep step

	// UI components
	spinner      spinner.Model
	progress     progress.Model
	qeList       list.Model
	techList     list.Model
	techInput    textinput.Model
	summaryInput textarea.Model

	// Data storage
	cardData        []CardData
	techDomains     []string
	customTechInput bool
	browserOpened   bool
	comparison      *sprintComparison
	previousCards   map[string]CardData // Map of previous sprint cards by key

	// Terminal dimensions
	terminalWidth  int
	terminalHeight int

	err error
}

type loadedMsg struct {
	cards []jira.Issue
}

type errorMsg struct {
	err error
}

type browserOpenedMsg struct{}

type clearBrowserOpenedMsg struct{}

func initialModel(jira jiraClient, filterName, outputFile, markdownFile, previousSprintFile string) model {
	s := spinner.New()
	s.Spinner = spinner.Points

	// Initialize progress bar
	prog := progress.New(progress.WithDefaultGradient())
	prog.Width = 80 // 2/3 of default 120 width, will be updated by window size

	// Initialize QE involvement list
	qeItems := make([]list.Item, len(qeOptions))
	for i, option := range qeOptions {
		qeItems[i] = listItem{title: option}
	}
	qeList := list.New(qeItems, list.NewDefaultDelegate(), 50, 10)
	qeList.Title = "QE Involvement"

	// Initialize tech domain list
	techDomains := make([]string, len(defaultTechDomains))
	copy(techDomains, defaultTechDomains)

	techItems := make([]list.Item, len(techDomains)+1)
	for i, domain := range techDomains {
		techItems[i] = listItem{title: domain}
	}
	techItems[len(techDomains)] = listItem{title: "Other (write-in)"}

	techList := list.New(techItems, list.NewDefaultDelegate(), 50, 10)
	techList.Title = "Technical Domain"

	// Initialize text input for custom tech domain
	techInput := textinput.New()
	techInput.Placeholder = "Enter technical domain"
	techInput.Width = 50

	// Initialize summary textarea
	summaryInput := textarea.New()
	summaryInput.Placeholder = "Enter a short summary (about 3 sentences) about the state of this card in the sprint..."
	summaryInput.SetWidth(80)
	summaryInput.SetHeight(5)

	return model{
		jira:               jira,
		filterName:         filterName,
		outputFile:         outputFile,
		markdownFile:       markdownFile,
		previousSprintFile: previousSprintFile,
		currentStep:        stepLoading,
		spinner:            s,
		progress:           prog,
		qeList:             qeList,
		techList:           techList,
		techInput:          techInput,
		summaryInput:       summaryInput,
		techDomains:        techDomains,
		terminalWidth:      120, // Default width, will be updated by window size messages
		terminalHeight:     30,  // Default height
	}
}

type listItem struct {
	title string
}

func (i listItem) Title() string       { return i.title }
func (i listItem) Description() string { return "" }
func (i listItem) FilterValue() string { return i.title }

func loadCards(jira jiraClient, filterName string) tea.Cmd {
	return func() tea.Msg {
		// Query for current sprint with filter
		jql := fmt.Sprintf(`Sprint in openSprints() AND filter = "%s"`, filterName)

		issues, _, err := jira.SearchWithContext(context.Background(), jql, nil)
		if err != nil {
			return errorMsg{err: err}
		}

		return loadedMsg{cards: issues}
	}
}

func loadExistingYAML(filename string) map[string]CardData {
	existingCards := make(map[string]CardData)

	data, err := os.ReadFile(filename)
	if err != nil {
		// File doesn't exist or can't be read, return empty map
		return existingCards
	}

	var summary SprintSummary
	if err := yaml.Unmarshal(data, &summary); err != nil {
		// Invalid YAML, return empty map
		return existingCards
	}

	for _, card := range summary.Cards {
		card.prefilled = true
		existingCards[card.Key] = card
	}

	return existingCards
}

func loadPreviousSprintData(filename string) ([]CardData, error) {
	if filename == "" {
		return nil, nil
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read previous sprint file: %w", err)
	}

	var summary SprintSummary
	if err := yaml.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("failed to parse previous sprint YAML: %w", err)
	}

	return summary.Cards, nil
}

func compareSprintData(previousCards []CardData, currentCards []jira.Issue) sprintComparison {
	// Create a map of current cards for quick lookup
	currentCardKeys := make(map[string]bool)
	for _, card := range currentCards {
		currentCardKeys[card.Key] = true
	}

	// Create a map of previous cards for quick lookup
	previousCardMap := make(map[string]CardData)
	for _, card := range previousCards {
		previousCardMap[card.Key] = card
	}

	var comparison sprintComparison

	// Find completed/abandoned cards (in previous but not in current)
	for _, previousCard := range previousCards {
		if !currentCardKeys[previousCard.Key] {
			comparison.completed = append(comparison.completed, previousCard)
		}
	}

	// Find carryover and new cards
	for _, currentCard := range currentCards {
		if _, exists := previousCardMap[currentCard.Key]; exists {
			// Carryover card - exists in both sprints
			comparison.carryover = append(comparison.carryover, CardData{
				Key:   currentCard.Key,
				Title: currentCard.Fields.Summary,
			})
		} else {
			// New card - only in current sprint
			comparison.new = append(comparison.new, CardData{
				Key:   currentCard.Key,
				Title: currentCard.Fields.Summary,
			})
		}
	}

	return comparison
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadCards(m.jira, m.filterName),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.cards = msg.cards
		m.cardData = make([]CardData, len(m.cards))

		// Load existing YAML data if available
		existingCards := loadExistingYAML(m.outputFile)

		// Initialize card data with basic info and merge existing data
		for i, card := range m.cards {
			cardURL, _ := url.JoinPath(m.jira.JiraURL(), "browse", card.Key)
			m.cardData[i] = CardData{
				Key:   card.Key,
				URL:   cardURL,
				Title: card.Fields.Summary,
			}

			// If this card exists in previous YAML, merge the data
			if existingCard, exists := existingCards[card.Key]; exists {
				m.cardData[i].QEInvolvement = existingCard.QEInvolvement
				m.cardData[i].TechDomain = existingCard.TechDomain
				m.cardData[i].Summary = existingCard.Summary
				m.cardData[i].Skipped = existingCard.Skipped
				m.cardData[i].Final = existingCard.Final
				m.cardData[i].prefilled = true
			}
		}

		// Check if we need to show sprint comparison
		if m.previousSprintFile != "" {
			previousCards, err := loadPreviousSprintData(m.previousSprintFile)
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			if previousCards != nil {
				// Store previous cards data for reference during editing
				m.previousCards = make(map[string]CardData)
				for _, card := range previousCards {
					m.previousCards[card.Key] = card
				}

				comparison := compareSprintData(previousCards, m.cards)
				m.comparison = &comparison
				m.currentStep = stepComparison
				return m, nil
			}
		}

		if len(m.cards) == 0 {
			m.currentStep = stepComplete
		} else {
			m.currentCard = 0
			m.currentStep = stepQEInvolvement
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.terminalWidth = msg.Width
		m.terminalHeight = msg.Height
		// Update progress bar width to 2/3 of terminal width
		progressWidth := (msg.Width * 2) / 3
		if progressWidth < 20 {
			progressWidth = 20 // Minimum width
		}
		m.progress.Width = progressWidth
		return m, nil

	case errorMsg:
		m.err = msg.err
		return m, tea.Quit

	case browserOpenedMsg:
		m.browserOpened = true
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return clearBrowserOpenedMsg{}
		})

	case clearBrowserOpenedMsg:
		m.browserOpened = false
		return m, nil

	case tea.KeyMsg:
		switch m.currentStep {
		case stepComparison:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "enter", " ":
				// Continue to editing mode
				if len(m.cards) == 0 {
					m.currentStep = stepComplete
				} else {
					m.currentCard = 0
					m.currentStep = stepQEInvolvement
				}
				return m, nil
			}
		case stepQEInvolvement:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "o":
				return m, m.openBrowser()
			case "e":
				// Edit this card even if prefilled
				if m.cardData[m.currentCard].prefilled {
					m.cardData[m.currentCard].prefilled = false
					m.cardData[m.currentCard].Skipped = false

					// Pre-select QE involvement in the list
					m.preselectQEInvolvement(m.cardData[m.currentCard].QEInvolvement)

					// Add tech domain to available domains if not already present and pre-select it
					m.addTechDomain(m.cardData[m.currentCard].TechDomain)
					m.preselectTechDomain(m.cardData[m.currentCard].TechDomain)

					// Pre-fill summary text area
					m.summaryInput.SetValue(m.cardData[m.currentCard].Summary)
				}
				return m, nil
			case "left", "h":
				// Navigate to previous card
				if m.currentCard > 0 {
					m.currentCard--
					// Reload saved data for the card we're navigating to
					m.reloadCardData(m.currentCard)
				}
				return m, nil
			case "right", "l":
				// Navigate to next card
				if m.currentCard < len(m.cardData)-1 {
					m.currentCard++
					// Reload saved data for the card we're navigating to
					m.reloadCardData(m.currentCard)
				}
				return m, nil
			case "f":
				// Toggle final status
				m.cardData[m.currentCard].Final = !m.cardData[m.currentCard].Final
				return m, m.savePartialResults()
			case "s":
				// Skip this card - mark as skipped and move to next card
				m.cardData[m.currentCard].Skipped = true
				m.currentCard++

				// Skip to next non-prefilled card
				for m.currentCard < len(m.cardData) && m.cardData[m.currentCard].prefilled {
					m.currentCard++
				}

				if m.currentCard >= len(m.cards) {
					m.currentStep = stepComplete
					return m, m.saveResults()
				} else {
					return m, m.savePartialResults()
				}
			case "enter":
				// Only allow enter to edit if card is not prefilled
				if !m.cardData[m.currentCard].prefilled {
					if selected := m.qeList.SelectedItem(); selected != nil {
						m.cardData[m.currentCard].QEInvolvement = selected.(listItem).title
						m.currentStep = stepTechDomain
						return m, nil
					}
				}
			case "esc":
				// Cancel edit mode and restore prefilled state
				if !m.cardData[m.currentCard].prefilled {
					// Find this card in existing data and restore it
					existingCards := loadExistingYAML(m.outputFile)
					if existingCard, exists := existingCards[m.cardData[m.currentCard].Key]; exists {
						m.cardData[m.currentCard] = existingCard
						m.cardData[m.currentCard].prefilled = true
					} else {
						// If no existing data, just mark as prefilled and clear data
						m.cardData[m.currentCard].QEInvolvement = ""
						m.cardData[m.currentCard].TechDomain = ""
						m.cardData[m.currentCard].Summary = ""
						m.cardData[m.currentCard].Skipped = false
						m.cardData[m.currentCard].prefilled = true
					}
				}
				return m, nil
			}

		case stepTechDomain:
			if m.customTechInput {
				switch msg.String() {
				case "ctrl+c":
					return m, tea.Quit
				case "enter":
					newDomain := strings.TrimSpace(m.techInput.Value())
					if newDomain != "" {
						m.cardData[m.currentCard].TechDomain = newDomain

						// Add to available domains for future cards
						found := false
						for _, existing := range m.techDomains {
							if existing == newDomain {
								found = true
								break
							}
						}
						if !found {
							m.techDomains = append(m.techDomains, newDomain)
							// Update the tech list with new option
							m.updateTechList()
						}

						m.customTechInput = false
						m.techInput.SetValue("")
						m.currentStep = stepSummary
						m.summaryInput.Focus()
						return m, nil
					}
				case "esc":
					m.customTechInput = false
					m.techInput.SetValue("")
					return m, nil
				}
			} else {
				switch msg.String() {
				case "q", "ctrl+c":
					return m, tea.Quit
				case "o":
					return m, m.openBrowser()
				case "left", "h":
					// Navigate to previous card
					if m.currentCard > 0 {
						m.currentCard--
						// Reload saved data for the card we're navigating to
						m.reloadCardData(m.currentCard)
					}
					return m, nil
				case "right", "l":
					// Navigate to next card
					if m.currentCard < len(m.cardData)-1 {
						m.currentCard++
						// Reload saved data for the card we're navigating to
						m.reloadCardData(m.currentCard)
					}
					return m, nil
				case "f":
					// Toggle final status
					m.cardData[m.currentCard].Final = !m.cardData[m.currentCard].Final
					return m, m.savePartialResults()
				case "enter":
					// Only allow enter to edit if card is not prefilled
					if !m.cardData[m.currentCard].prefilled {
						if selected := m.techList.SelectedItem(); selected != nil {
							selectedTitle := selected.(listItem).title
							if selectedTitle == "Other (write-in)" {
								m.customTechInput = true
								m.techInput.Focus()
								return m, nil
							} else {
								m.cardData[m.currentCard].TechDomain = selectedTitle
								m.currentStep = stepSummary
								m.summaryInput.Focus()
								return m, nil
							}
						}
					}
				case "esc":
					// Cancel edit mode and restore prefilled state
					if !m.cardData[m.currentCard].prefilled {
						// Find this card in existing data and restore it
						existingCards := loadExistingYAML(m.outputFile)
						if existingCard, exists := existingCards[m.cardData[m.currentCard].Key]; exists {
							m.cardData[m.currentCard] = existingCard
							m.cardData[m.currentCard].prefilled = true
						} else {
							// If no existing data, just mark as prefilled and clear data
							m.cardData[m.currentCard].QEInvolvement = ""
							m.cardData[m.currentCard].TechDomain = ""
							m.cardData[m.currentCard].Summary = ""
							m.cardData[m.currentCard].Skipped = false
							m.cardData[m.currentCard].prefilled = true
						}
						// Go back to QE involvement step
						m.currentStep = stepQEInvolvement
					}
					return m, nil
				}
			}

		case stepSummary:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				// Cancel edit mode and restore prefilled state
				if !m.cardData[m.currentCard].prefilled {
					// Find this card in existing data and restore it
					existingCards := loadExistingYAML(m.outputFile)
					if existingCard, exists := existingCards[m.cardData[m.currentCard].Key]; exists {
						m.cardData[m.currentCard] = existingCard
						m.cardData[m.currentCard].prefilled = true
					} else {
						// If no existing data, just mark as prefilled and clear data
						m.cardData[m.currentCard].QEInvolvement = ""
						m.cardData[m.currentCard].TechDomain = ""
						m.cardData[m.currentCard].Summary = ""
						m.cardData[m.currentCard].Skipped = false
						m.cardData[m.currentCard].prefilled = true
					}
					// Clear and blur the summary input
					m.summaryInput.SetValue("")
					m.summaryInput.Blur()
					// Go back to QE involvement step
					m.currentStep = stepQEInvolvement
				}
				return m, nil
			case "ctrl+s":
				summary := strings.TrimSpace(m.summaryInput.Value())
				if summary != "" {
					m.cardData[m.currentCard].Summary = summary
					// Mark the card as prefilled since it's now been completed
					m.cardData[m.currentCard].prefilled = true
					m.summaryInput.SetValue("")
					m.summaryInput.Blur()

					// Move to next card
					m.currentCard++

					// Skip to next non-prefilled card
					for m.currentCard < len(m.cardData) && m.cardData[m.currentCard].prefilled {
						m.currentCard++
					}

					if m.currentCard >= len(m.cards) {
						m.currentStep = stepComplete
						return m, m.saveResults()
					} else {
						m.currentStep = stepQEInvolvement
						return m, m.savePartialResults()
					}
				}
			}
		}
	}

	// Update components based on current step
	var cmd tea.Cmd
	switch m.currentStep {
	case stepLoading:
		m.spinner, cmd = m.spinner.Update(msg)
	case stepQEInvolvement:
		m.qeList, cmd = m.qeList.Update(msg)
	case stepTechDomain:
		if m.customTechInput {
			m.techInput, cmd = m.techInput.Update(msg)
		} else {
			m.techList, cmd = m.techList.Update(msg)
		}
	case stepSummary:
		m.summaryInput, cmd = m.summaryInput.Update(msg)
	}

	return m, cmd
}

func (m *model) updateTechList() {
	techItems := make([]list.Item, len(m.techDomains)+1)
	for i, domain := range m.techDomains {
		techItems[i] = listItem{title: domain}
	}
	techItems[len(m.techDomains)] = listItem{title: "Other (write-in)"}
	m.techList.SetItems(techItems)
}

func (m model) openBrowser() tea.Cmd {
	return func() tea.Msg {
		if m.currentCard < len(m.cardData) {
			_ = exec.Command("xdg-open", m.cardData[m.currentCard].URL).Start()
			return browserOpenedMsg{}
		}
		return nil
	}
}

func (m *model) preselectQEInvolvement(qeInvolvement string) {
	// Find the index of the QE involvement option
	targetIndex := -1
	for i, option := range qeOptions {
		if option == qeInvolvement {
			targetIndex = i
			break
		}
	}

	if targetIndex >= 0 {
		// Reset cursor to top first
		for i := 0; i < len(qeOptions); i++ {
			m.qeList.CursorUp()
		}
		// Move to target position
		for i := 0; i < targetIndex; i++ {
			m.qeList.CursorDown()
		}
	}
}

func (m *model) preselectTechDomain(techDomain string) {
	if techDomain == "" {
		return
	}

	// Find the index of the tech domain option
	targetIndex := -1
	for i, domain := range m.techDomains {
		if domain == techDomain {
			targetIndex = i
			break
		}
	}

	if targetIndex >= 0 {
		// Reset cursor to top first
		for i := 0; i < len(m.techDomains)+1; i++ { // +1 for "Other" option
			m.techList.CursorUp()
		}
		// Move to target position
		for i := 0; i < targetIndex; i++ {
			m.techList.CursorDown()
		}
	}
}

func (m *model) addTechDomain(techDomain string) {
	if techDomain == "" {
		return
	}

	// Check if domain already exists
	for _, existing := range m.techDomains {
		if existing == techDomain {
			return // Already exists
		}
	}

	// Add to available domains
	m.techDomains = append(m.techDomains, techDomain)
	m.updateTechList()
}

func (m *model) reloadCardData(cardIndex int) {
	// Reload saved data for the specific card
	existingCards := loadExistingYAML(m.outputFile)
	cardKey := m.cardData[cardIndex].Key

	if existingCard, exists := existingCards[cardKey]; exists {
		// Preserve the Key, URL, and Title from current data
		key := m.cardData[cardIndex].Key
		url := m.cardData[cardIndex].URL
		title := m.cardData[cardIndex].Title

		// Update with saved data
		m.cardData[cardIndex] = existingCard
		m.cardData[cardIndex].Key = key
		m.cardData[cardIndex].URL = url
		m.cardData[cardIndex].Title = title
		m.cardData[cardIndex].prefilled = true
	}
}

func generateMarkdownSummary(cardData []CardData) string {
	// Group cards by QE involvement, then by technical domain
	qeGroups := make(map[string]map[string][]CardData)

	for _, card := range cardData {
		// Only include cards that have been processed (not skipped or have QE involvement)
		if card.QEInvolvement == "" && !card.Skipped {
			continue
		}

		if card.Skipped {
			continue // Skip cards entirely
		}

		if qeGroups[card.QEInvolvement] == nil {
			qeGroups[card.QEInvolvement] = make(map[string][]CardData)
		}

		qeGroups[card.QEInvolvement][card.TechDomain] = append(qeGroups[card.QEInvolvement][card.TechDomain], card)
	}

	var markdown strings.Builder
	markdown.WriteString("# Sprint Summary\n\n")

	// Order QE involvement sections
	qeOrder := []string{"Needs QE involvement", "Needs QE awareness", "OSUS Operations", "QE involvement not needed"}

	for _, qeInvolvement := range qeOrder {
		techDomains, exists := qeGroups[qeInvolvement]
		if !exists || len(techDomains) == 0 {
			continue
		}

		markdown.WriteString(fmt.Sprintf("# %s\n\n", qeInvolvement))

		// Sort technical domains alphabetically
		var sortedDomains []string
		for domain := range techDomains {
			sortedDomains = append(sortedDomains, domain)
		}
		sort.Strings(sortedDomains)

		for _, domain := range sortedDomains {
			cards := techDomains[domain]
			if len(cards) == 0 {
				continue
			}

			markdown.WriteString(fmt.Sprintf("## %s\n\n", domain))

			for _, card := range cards {
				markdown.WriteString(fmt.Sprintf("[%s](%s)\n\n", card.Key, card.URL))
				if card.Summary != "" {
					markdown.WriteString(fmt.Sprintf("%s\n\n", card.Summary))
				}
			}
		}
	}

	return markdown.String()
}

func (m model) savePartialResults() tea.Cmd {
	return func() tea.Msg {
		// Include all processed cards (completed and skipped)
		var processedCards []CardData
		for _, card := range m.cardData {
			if card.QEInvolvement != "" || card.Skipped {
				processedCards = append(processedCards, card)
			}
		}

		summary := SprintSummary{Cards: processedCards}

		data, err := yaml.Marshal(summary)
		if err != nil {
			return errorMsg{err: err}
		}

		if err := os.WriteFile(m.outputFile, data, 0644); err != nil {
			return errorMsg{err: err}
		}

		// Generate and save markdown summary
		if m.markdownFile != "" {
			markdownContent := generateMarkdownSummary(m.cardData)
			if err := os.WriteFile(m.markdownFile, []byte(markdownContent), 0644); err != nil {
				return errorMsg{err: err}
			}
		}

		return nil
	}
}

func (m model) saveResults() tea.Cmd {
	return m.savePartialResults()
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1).
			MarginBottom(1)

	progressStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))
)

func formatKeyValue(key, value string, width int) string {
	// Create the key with colon and calculate needed padding
	keyWithColon := key + ":"
	padding := width - len(keyWithColon)
	if padding < 1 {
		padding = 1
	}

	// Create the full label with padding first
	labelWithPadding := keyWithColon + strings.Repeat(" ", padding)

	// Apply style to the entire padded label
	styledLabel := labelStyle.Render(labelWithPadding)

	return styledLabel + value
}

func formatKeyValueWithWrap(key, value string, labelWidth, terminalWidth int) string {
	// Create the key with colon and calculate needed padding
	keyWithColon := key + ":"
	padding := labelWidth - len(keyWithColon)
	if padding < 1 {
		padding = 1
	}

	// Create the full label with padding first
	labelWithPadding := keyWithColon + strings.Repeat(" ", padding)

	// Apply style to the entire padded label
	styledLabel := labelStyle.Render(labelWithPadding)

	// Calculate available width for text (account for ANSI escape codes in styled label)
	// The styled label visual width is just the labelWidth
	availableWidth := terminalWidth - labelWidth
	if availableWidth < 20 {
		availableWidth = 20 // Minimum reasonable width
	}

	// Wrap the value text
	wrappedValue := wrapText(value, availableWidth)

	// For multiline values, indent continuation lines
	lines := strings.Split(wrappedValue, "\n")
	if len(lines) > 1 {
		indent := strings.Repeat(" ", labelWidth)
		for i := 1; i < len(lines); i++ {
			lines[i] = indent + lines[i]
		}
		wrappedValue = strings.Join(lines, "\n")
	}

	return styledLabel + lines[0] + (func() string {
		if len(lines) > 1 {
			return "\n" + strings.Join(lines[1:], "\n")
		}
		return ""
	})()
}

func wrapText(text string, width int) string {
	if len(text) <= width {
		return text
	}

	var result []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	currentLine := words[0]
	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			result = append(result, currentLine)
			currentLine = word
		}
	}
	result = append(result, currentLine)

	return strings.Join(result, "\n")
}

func renderComparisonTables(comparison sprintComparison, terminalWidth int) string {
	var result strings.Builder

	// Table style
	tableStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1).
		MarginBottom(1)

	sectionTitleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)

	// Calculate table width (1/3 of terminal width each, with margins)
	tableWidth := (terminalWidth - 12) / 3 // Account for borders and margins
	if tableWidth < 30 {
		tableWidth = 30
	}

	// Completed/Abandoned cards
	completedTable := sectionTitleStyle.Render("Completed/Abandoned") + "\n"
	if len(comparison.completed) == 0 {
		completedTable += "(None)"
	} else {
		for _, card := range comparison.completed {
			completedTable += fmt.Sprintf("%s: %s\n", card.Key, wrapText(card.Title, tableWidth-10))
		}
	}

	// Carryover cards
	carryoverTable := sectionTitleStyle.Render("Carryover") + "\n"
	if len(comparison.carryover) == 0 {
		carryoverTable += "(None)"
	} else {
		for _, card := range comparison.carryover {
			carryoverTable += fmt.Sprintf("%s: %s\n", card.Key, wrapText(card.Title, tableWidth-10))
		}
	}

	// New cards
	newTable := sectionTitleStyle.Render("New Cards") + "\n"
	if len(comparison.new) == 0 {
		newTable += "(None)"
	} else {
		for _, card := range comparison.new {
			newTable += fmt.Sprintf("%s: %s\n", card.Key, wrapText(card.Title, tableWidth-10))
		}
	}

	// Apply table styling
	styledCompleted := tableStyle.Copy().Width(tableWidth).Render(completedTable)
	styledCarryover := tableStyle.Copy().Width(tableWidth).Render(carryoverTable)
	styledNew := tableStyle.Copy().Width(tableWidth).Render(newTable)

	// Arrange tables side by side
	result.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, styledCompleted, styledCarryover, styledNew))

	return result.String()
}

func renderPreviousSprintInfo(previousCard CardData, terminalWidth int) string {
	if previousCard.Key == "" {
		return "" // No previous sprint data
	}

	sectionStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(1).
		MarginBottom(1).
		Foreground(lipgloss.Color("240"))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("33"))

	var content strings.Builder
	content.WriteString(titleStyle.Render("Previous Sprint Information:") + "\n\n")

	if previousCard.Skipped {
		content.WriteString("This card was skipped in the previous sprint.")
	} else {
		// Calculate label width for alignment
		labels := []string{"QE Involvement", "Tech Domain", "Summary"}
		labelWidth := 0
		for _, label := range labels {
			if len(label) > labelWidth {
				labelWidth = len(label)
			}
		}
		labelWidth += 2 // Add space for colon and padding

		content.WriteString(fmt.Sprintf("%s\n", formatKeyValue("QE Involvement", previousCard.QEInvolvement, labelWidth)))
		content.WriteString(fmt.Sprintf("%s\n", formatKeyValue("Tech Domain", previousCard.TechDomain, labelWidth)))
		if previousCard.Summary != "" {
			// Use a smaller width for the summary to fit in the info box
			availableWidth := (terminalWidth * 2) / 5 // Use about 40% of terminal width
			if availableWidth < 40 {
				availableWidth = 40
			}
			content.WriteString(formatKeyValueWithWrap("Summary", previousCard.Summary, labelWidth, availableWidth))
		}
	}

	// Apply styling and center
	infoBox := sectionStyle.Render(content.String())

	centeredStyle := lipgloss.NewStyle().
		Width(terminalWidth).
		Align(lipgloss.Center)

	return centeredStyle.Render(infoBox)
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\nPress any key to exit.", m.err)
	}

	switch m.currentStep {
	case stepLoading:
		return fmt.Sprintf("%s Loading sprint cards...", m.spinner.View())

	case stepComparison:
		if m.comparison == nil {
			return "No comparison data available."
		}

		title := titleStyle.Render("Sprint Comparison")
		comparisonView := renderComparisonTables(*m.comparison, m.terminalWidth)
		instructions := progressStyle.Render("Press Enter or Space to continue to editing mode, q to quit")

		// Center the title and instructions
		centeredTitleStyle := lipgloss.NewStyle().
			Width(m.terminalWidth).
			Align(lipgloss.Center)
		centeredTitle := centeredTitleStyle.Render(title)

		centeredInstructionsStyle := lipgloss.NewStyle().
			Width(m.terminalWidth).
			Align(lipgloss.Center)
		centeredInstructions := centeredInstructionsStyle.Render(instructions)

		return fmt.Sprintf("%s\n\n%s\n\n%s", centeredTitle, comparisonView, centeredInstructions)

	case stepComplete:
		if len(m.cards) == 0 {
			return "No cards found in current sprint."
		}
		completedCount := 0
		skippedCount := 0
		for _, card := range m.cardData {
			if card.Skipped {
				skippedCount++
			} else if card.QEInvolvement != "" {
				completedCount++
			}
		}
		return fmt.Sprintf("Summary saved to %s!\n\nCompleted %d cards, skipped %d cards.", m.outputFile, completedCount, skippedCount)
	}

	if len(m.cards) == 0 {
		return "No cards to process."
	}

	// Current card info
	currentCard := m.cards[m.currentCard]

	// Calculate progress - set to 100% when on the last card
	progressPercent := float64(m.currentCard) / float64(len(m.cards))
	if m.currentCard == len(m.cards)-1 {
		progressPercent = 1.0 // 100% when on the last card
	}
	progressText := fmt.Sprintf("Card %d of %d", m.currentCard+1, len(m.cards))
	progressBar := m.progress.ViewAs(progressPercent)

	// Combine progress text and bar, then center them
	progressDisplay := fmt.Sprintf("%s\n%s", progressText, progressBar)

	// Create a centered progress display using lipgloss
	centeredProgressStyle := lipgloss.NewStyle().
		Width(m.terminalWidth).
		Align(lipgloss.Center)
	progressDisplay = centeredProgressStyle.Render(progressDisplay)

	assignee := "Unassigned"
	if currentCard.Fields.Assignee != nil {
		assignee = currentCard.Fields.Assignee.DisplayName
	}

	status := "Unknown"
	if currentCard.Fields.Status != nil {
		status = currentCard.Fields.Status.Name
	}

	cardType := "Unknown"
	if currentCard.Fields.Type.Name != "" {
		cardType = currentCard.Fields.Type.Name
	}

	// Add prefilled and final indicators
	prefillIndicator := ""
	if m.cardData[m.currentCard].prefilled {
		if m.cardData[m.currentCard].Skipped {
			prefillIndicator = " ⏭️  (Previously skipped)"
		} else {
			prefillIndicator = " ✅ (Previously completed)"
		}
	}

	// Add final indicator
	if m.cardData[m.currentCard].Final {
		prefillIndicator += " 🏁 (Final)"
	}

	// Format card info with aligned values
	cardLabels := []string{"Key", "Title", "Assignee", "Status", "Type"}
	labelWidth := 0
	for _, label := range cardLabels {
		if len(label) > labelWidth {
			labelWidth = len(label)
		}
	}
	labelWidth += 2 // Add space for colon and padding

	cardInfoText := fmt.Sprintf("%s\n%s\n%s\n%s\n%s",
		formatKeyValue("Key", currentCard.Key+prefillIndicator, labelWidth),
		formatKeyValue("Title", currentCard.Fields.Summary, labelWidth),
		formatKeyValue("Assignee", assignee, labelWidth),
		formatKeyValue("Status", status, labelWidth),
		formatKeyValue("Type", cardType, labelWidth),
	)
	// Create a fixed-width card style based on terminal size
	cardWidth := (m.terminalWidth * 3) / 4 // Use 3/4 of terminal width
	if cardWidth < 60 {
		cardWidth = 60 // Minimum width
	}
	if cardWidth > 120 {
		cardWidth = 120 // Maximum width for readability
	}

	// Create card with fixed width and center it
	dynamicCardStyle := cardStyle.Copy().Width(cardWidth)

	// Use green border for final cards
	if m.cardData[m.currentCard].Final {
		dynamicCardStyle = dynamicCardStyle.BorderForeground(lipgloss.Color("46")) // Green border
	}

	cardInfo := dynamicCardStyle.Render(cardInfoText)

	// Center the entire card frame
	cardCenterStyle := lipgloss.NewStyle().
		Width(m.terminalWidth).
		Align(lipgloss.Center)
	cardInfo = cardCenterStyle.Render(cardInfo)

	var content string
	var instructions string
	var previousSprintInfo string

	// Get previous sprint information for this card if available
	if m.previousCards != nil {
		if prevCard, exists := m.previousCards[m.cards[m.currentCard].Key]; exists {
			previousSprintInfo = renderPreviousSprintInfo(prevCard, m.terminalWidth)
		}
	}

	switch m.currentStep {
	case stepQEInvolvement:
		if m.cardData[m.currentCard].prefilled {
			// Show prefilled data
			if m.cardData[m.currentCard].Skipped {
				content = "This card was previously skipped."
				instructions = "←/→ (h/l) to navigate cards, 'e' to edit, 'f' to toggle final, 'o' to open in browser, q to quit"
			} else {
				prefilledLabels := []string{"QE Involvement", "Tech Domain", "Summary"}
				prefilledLabelWidth := 0
				for _, label := range prefilledLabels {
					if len(label) > prefilledLabelWidth {
						prefilledLabelWidth = len(label)
					}
				}
				prefilledLabelWidth += 2 // Add space for colon and padding

				content = fmt.Sprintf("Previously completed:\n%s\n%s\n%s",
					formatKeyValue("QE Involvement", m.cardData[m.currentCard].QEInvolvement, prefilledLabelWidth),
					formatKeyValue("Tech Domain", m.cardData[m.currentCard].TechDomain, prefilledLabelWidth),
					formatKeyValueWithWrap("Summary", m.cardData[m.currentCard].Summary, prefilledLabelWidth, m.terminalWidth))
				instructions = "←/→ (h/l) to navigate cards, 'e' to edit, 'f' to toggle final, 'o' to open in browser, q to quit"
			}
		} else {
			content = m.qeList.View()
			instructions = "Use ↑/↓ to navigate options, ←/→ (h/l) to navigate cards, Enter to select, 'f' to toggle final, 'o' to open in browser, 'e' to edit prefilled, 's' to skip card, q to quit"
		}

	case stepTechDomain:
		if m.customTechInput {
			content = fmt.Sprintf("Enter technical domain:\n\n%s", m.techInput.View())
			instructions = "Type domain name, Enter to confirm, Esc to cancel"
		} else {
			content = m.techList.View()
			instructions = "Use ↑/↓ to navigate options, ←/→ (h/l) to navigate cards, Enter to select, 'f' to toggle final, 'o' to open in browser, Esc to cancel edit, q to quit"
		}

	case stepSummary:
		content = fmt.Sprintf("Enter summary (about 3 sentences):\n\n%s", m.summaryInput.View())
		instructions = "Ctrl+S to save and continue, Esc to cancel edit, Ctrl+C to quit"
	}

	statusMsg := ""
	if m.browserOpened {
		statusMsg = progressStyle.Render("🌐 Opened in browser") + "\n\n"
	}

	// Center the title and instructions using lipgloss
	centeredTitleStyle := lipgloss.NewStyle().
		Width(m.terminalWidth).
		Align(lipgloss.Center)
	centeredTitle := centeredTitleStyle.Render(titleStyle.Render("Sprint Summary Tool"))

	centeredInstructionsStyle := lipgloss.NewStyle().
		Width(m.terminalWidth).
		Align(lipgloss.Center)
	centeredInstructions := centeredInstructionsStyle.Render(progressStyle.Render(instructions))

	// Build the view components
	var viewParts []string
	viewParts = append(viewParts, centeredTitle)
	viewParts = append(viewParts, progressStyle.Render(progressDisplay))
	viewParts = append(viewParts, cardInfo)

	// Add previous sprint information if available
	if previousSprintInfo != "" {
		viewParts = append(viewParts, previousSprintInfo)
	}

	if statusMsg != "" {
		viewParts = append(viewParts, statusMsg+content)
	} else {
		viewParts = append(viewParts, content)
	}

	viewParts = append(viewParts, centeredInstructions)

	return strings.Join(viewParts, "\n\n")
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	o.jira.AddFlags(fs)
	fs.StringVar(&o.filter, "filter", "Filter for OTA", "Jira filter name")
	fs.StringVar(&o.output, "output", "/tmp/sprint-summary.yaml", "Output YAML file")
	fs.StringVar(&o.markdown, "markdown", "/tmp/sprint-summary.md", "Output markdown file")
	fs.StringVar(&o.previousSprint, "previous-sprint", "", "Previous sprint YAML artifact for comparison")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args")
	}

	return o
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	jiraClient, err := o.jira.Client()
	if err != nil {
		logrus.WithError(err).Fatal("cannot create Jira client")
	}

	model := initialModel(jiraClient, o.filter, o.output, o.markdown, o.previousSprint)

	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}
