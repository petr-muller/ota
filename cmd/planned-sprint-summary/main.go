package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/bubbles/list"
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
	jira   flagutil.JiraOptions
	filter string
	output string
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
	
	// Non-exported field to track if this card was prefilled from existing YAML
	prefilled bool
}

type SprintSummary struct {
	Cards []CardData `yaml:"cards"`
}

type jiraClient interface {
	SearchWithContext(context.Context, string, *jira.SearchOptions) ([]jira.Issue, *jira.Response, error)
	JiraURL() string
}

type step int

const (
	stepLoading step = iota
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
	jira       jiraClient
	filterName string
	outputFile string

	cards       []jira.Issue
	currentCard int
	currentStep step

	// UI components
	spinner      spinner.Model
	qeList       list.Model
	techList     list.Model
	techInput    textinput.Model
	summaryInput textarea.Model

	// Data storage
	cardData        []CardData
	techDomains     []string
	customTechInput bool
	browserOpened   bool

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

func initialModel(jira jiraClient, filterName, outputFile string) model {
	s := spinner.New()
	s.Spinner = spinner.Points

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
		jira:         jira,
		filterName:   filterName,
		outputFile:   outputFile,
		currentStep:  stepLoading,
		spinner:      s,
		qeList:       qeList,
		techList:     techList,
		techInput:    techInput,
		summaryInput: summaryInput,
		techDomains:  techDomains,
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
				m.cardData[i].prefilled = true
			}
		}

		if len(m.cards) == 0 {
			m.currentStep = stepComplete
		} else {
			m.currentCard = 0
			m.currentStep = stepQEInvolvement
		}
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
					// Clear the prefilled data to allow fresh input
					m.cardData[m.currentCard].QEInvolvement = ""
					m.cardData[m.currentCard].TechDomain = ""
					m.cardData[m.currentCard].Summary = ""
					m.cardData[m.currentCard].Skipped = false
				}
				return m, nil
			case "left", "h":
				// Navigate to previous card
				if m.currentCard > 0 {
					m.currentCard--
				}
				return m, nil
			case "right", "l":
				// Navigate to next card
				if m.currentCard < len(m.cardData)-1 {
					m.currentCard++
				}
				return m, nil
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
				if selected := m.qeList.SelectedItem(); selected != nil {
					m.cardData[m.currentCard].QEInvolvement = selected.(listItem).title
					m.currentStep = stepTechDomain
					return m, nil
				}
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
					}
					return m, nil
				case "right", "l":
					// Navigate to next card
					if m.currentCard < len(m.cardData)-1 {
						m.currentCard++
					}
					return m, nil
				case "enter":
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
			}

		case stepSummary:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "o":
				return m, m.openBrowser()
			case "left", "h":
				// Navigate to previous card
				if m.currentCard > 0 {
					m.currentCard--
				}
				return m, nil
			case "right", "l":
				// Navigate to next card
				if m.currentCard < len(m.cardData)-1 {
					m.currentCard++
				}
				return m, nil
			case "ctrl+s":
				summary := strings.TrimSpace(m.summaryInput.Value())
				if summary != "" {
					m.cardData[m.currentCard].Summary = summary
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
)

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\nPress any key to exit.", m.err)
	}

	switch m.currentStep {
	case stepLoading:
		return fmt.Sprintf("%s Loading sprint cards...", m.spinner.View())

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
	progress := fmt.Sprintf("Card %d of %d", m.currentCard+1, len(m.cards))

	assignee := "Unassigned"
	if currentCard.Fields.Assignee != nil {
		assignee = currentCard.Fields.Assignee.DisplayName
	}

	status := "Unknown"
	if currentCard.Fields.Status != nil {
		status = currentCard.Fields.Status.Name
	}

	// Add prefilled indicator
	prefillIndicator := ""
	if m.cardData[m.currentCard].prefilled {
		if m.cardData[m.currentCard].Skipped {
			prefillIndicator = " ⏭️  (Previously skipped)"
		} else {
			prefillIndicator = " ✅ (Previously completed)"
		}
	}

	cardInfo := cardStyle.Render(fmt.Sprintf(
		"Key: %s%s\nTitle: %s\nAssignee: %s\nStatus: %s",
		currentCard.Key,
		prefillIndicator,
		currentCard.Fields.Summary,
		assignee,
		status,
	))

	var content string
	var instructions string

	switch m.currentStep {
	case stepQEInvolvement:
		if m.cardData[m.currentCard].prefilled {
			// Show prefilled data
			if m.cardData[m.currentCard].Skipped {
				content = "This card was previously skipped."
				instructions = "←/→ (h/l) to navigate cards, 'e' to edit, 'o' to open in browser, q to quit"
			} else {
				content = fmt.Sprintf("Previously completed:\nQE Involvement: %s\nTech Domain: %s\nSummary: %s", 
					m.cardData[m.currentCard].QEInvolvement,
					m.cardData[m.currentCard].TechDomain,
					m.cardData[m.currentCard].Summary)
				instructions = "←/→ (h/l) to navigate cards, 'e' to edit, 'o' to open in browser, q to quit"
			}
		} else {
			content = m.qeList.View()
			instructions = "Use ↑/↓ to navigate options, ←/→ (h/l) to navigate cards, Enter to select, 'o' to open in browser, 'e' to edit prefilled, 's' to skip card, q to quit"
		}

	case stepTechDomain:
		if m.customTechInput {
			content = fmt.Sprintf("Enter technical domain:\n\n%s", m.techInput.View())
			instructions = "Type domain name, Enter to confirm, Esc to cancel"
		} else {
			content = m.techList.View()
			instructions = "Use ↑/↓ to navigate options, ←/→ (h/l) to navigate cards, Enter to select, 'o' to open in browser, q to quit"
		}

	case stepSummary:
		content = fmt.Sprintf("Enter summary (about 3 sentences):\n\n%s", m.summaryInput.View())
		instructions = "Ctrl+S to save and continue, ←/→ (h/l) to navigate cards, 'o' to open in browser, Ctrl+C to quit"
	}

	statusMsg := ""
	if m.browserOpened {
		statusMsg = progressStyle.Render("🌐 Opened in browser") + "\n\n"
	}

	return fmt.Sprintf(
		"%s\n\n%s\n\n%s\n\n%s%s\n\n%s",
		titleStyle.Render("Sprint Summary Tool"),
		progressStyle.Render(progress),
		cardInfo,
		statusMsg,
		content,
		progressStyle.Render(instructions),
	)
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	o.jira.AddFlags(fs)
	fs.StringVar(&o.filter, "filter", "Filter for OTA", "Jira filter name")
	fs.StringVar(&o.output, "output", "sprint-summary.yaml", "Output YAML file")

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

	model := initialModel(jiraClient, o.filter, o.output)

	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}
