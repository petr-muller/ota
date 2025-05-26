package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

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
	Title         string `yaml:"title"`
	QEInvolvement string `yaml:"qe_involvement"`
	TechDomain    string `yaml:"technical_domain"`
	Summary       string `yaml:"summary"`
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

	err error
}

type loadedMsg struct {
	cards []jira.Issue
}

type errorMsg struct {
	err error
}

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

		// Initialize card data with basic info
		for i, card := range m.cards {
			cardURL, _ := url.JoinPath(m.jira.JiraURL(), "browse", card.Key)
			m.cardData[i] = CardData{
				Key:   card.Key,
				URL:   cardURL,
				Title: card.Fields.Summary,
			}
		}

		if len(m.cards) == 0 {
			m.currentStep = stepComplete
		} else {
			m.currentStep = stepQEInvolvement
		}
		return m, nil

	case errorMsg:
		m.err = msg.err
		return m, tea.Quit

	case tea.KeyMsg:
		switch m.currentStep {
		case stepQEInvolvement:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "s":
				// Skip this card - move to next card without collecting data
				m.currentCard++
				if m.currentCard >= len(m.cards) {
					m.currentStep = stepComplete
					return m, m.saveResults()
				}
				return m, nil
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
			case "ctrl+s":
				summary := strings.TrimSpace(m.summaryInput.Value())
				if summary != "" {
					m.cardData[m.currentCard].Summary = summary
					m.summaryInput.SetValue("")
					m.summaryInput.Blur()

					// Move to next card
					m.currentCard++
					if m.currentCard >= len(m.cards) {
						m.currentStep = stepComplete
						return m, m.saveResults()
					} else {
						m.currentStep = stepQEInvolvement
						return m, nil
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

func (m model) saveResults() tea.Cmd {
	return func() tea.Msg {
		// Filter out skipped cards (those without QE involvement data)
		var completedCards []CardData
		for _, card := range m.cardData {
			if card.QEInvolvement != "" {
				completedCards = append(completedCards, card)
			}
		}
		
		summary := SprintSummary{Cards: completedCards}

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
		for _, card := range m.cardData {
			if card.QEInvolvement != "" {
				completedCount++
			}
		}
		skippedCount := len(m.cards) - completedCount
		return fmt.Sprintf("Summary saved to %s!\n\nProcessed %d cards, skipped %d cards.", m.outputFile, completedCount, skippedCount)
	}

	if len(m.cards) == 0 {
		return "No cards to process."
	}

	// Current card info
	currentCard := m.cards[m.currentCard]
	progress := fmt.Sprintf("Card %d of %d", m.currentCard+1, len(m.cards))

	cardInfo := cardStyle.Render(fmt.Sprintf(
		"Key: %s\nTitle: %s",
		currentCard.Key,
		currentCard.Fields.Summary,
	))

	var content string
	var instructions string

	switch m.currentStep {
	case stepQEInvolvement:
		content = m.qeList.View()
		instructions = "Use ↑/↓ to navigate, Enter to select, 's' to skip card, q to quit"

	case stepTechDomain:
		if m.customTechInput {
			content = fmt.Sprintf("Enter technical domain:\n\n%s", m.techInput.View())
			instructions = "Type domain name, Enter to confirm, Esc to cancel"
		} else {
			content = m.techList.View()
			instructions = "Use ↑/↓ to navigate, Enter to select, q to quit"
		}

	case stepSummary:
		content = fmt.Sprintf("Enter summary (about 3 sentences):\n\n%s", m.summaryInput.View())
		instructions = "Ctrl+S to save and continue, Ctrl+C to quit"
	}

	return fmt.Sprintf(
		"%s\n\n%s\n\n%s\n\n%s\n\n%s",
		titleStyle.Render("Sprint Summary Tool"),
		progressStyle.Render(progress),
		cardInfo,
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
