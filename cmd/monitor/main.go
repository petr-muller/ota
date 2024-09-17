package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/petr-muller/ota/internal/flagutil"
)

type options struct {
	jira flagutil.JiraOptions
}

func (o *options) validate() error {
	return o.jira.Validate()
}

type optionsMsg options

type jiraClientMsg jiraClient

type jiraClient interface {
	SearchWithContext(context.Context, string, *jira.SearchOptions) ([]jira.Issue, *jira.Response, error)
}

type jiraItems struct {
	query   string
	fetched bool
	items   []jira.Issue
	table   table.Model
}

func (i jiraItems) View() string {
	if !i.fetched {
		return "Fetching..."
	}

	return i.table.View()
}

func initialModel() model {
	return model{
		needImpactStatementRequest: jiraItems{
			query: "project = OCPBUGS AND labels in (UpgradeBlocker) AND labels not in (ImpactStatementRequested, ImpactStatementProposed, UpdateRecommendationsBlocked)",
		},
	}
}

type needImpactStatementRequestMsg jiraItems

func refreshNeedImpactStatementRequest(jiras jiraItems, jira jiraClient) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()

		items, _, err := jira.SearchWithContext(context.Background(), jiras.query, nil)
		if err != nil {
			// TODO(muller): Something
		}
		jiras.items = items
		jiras.fetched = true

		lengths := [...]int{len("ID"), len("Summary"), len("Component"), len("Modified"), len("Affects")}
		var rows []table.Row
		for _, item := range items {
			var affects []string
			for _, version := range item.Fields.AffectsVersions {
				affects = append(affects, version.Name)
			}
			row := table.Row{
				item.Key,
				item.Fields.Summary,
				item.Fields.Components[0].Name,
				now.Sub(time.Time(item.Fields.Updated)).Truncate(time.Minute).String(),
				strings.Join(affects, "|"),
			}
			for i := range lengths {
				if length := len(row[i]); length > lengths[i] {
					lengths[i] = min(length, 75)
				}
			}
			rows = append(rows, row)
		}

		height := min(10, len(rows)+2)

		jiras.table = table.New(
			table.WithColumns(
				[]table.Column{
					{Width: lengths[0], Title: "ID"},
					{Width: lengths[1], Title: "Summary"},
					{Width: lengths[2], Title: "Component"},
					{Width: lengths[3], Title: "Modified"},
					{Width: lengths[4], Title: "Affects"},
				},
			),
			table.WithRows(rows),
			table.WithFocused(true),
			table.WithHeight(height),
		)
		return needImpactStatementRequestMsg(jiras)
	}
}

type model struct {
	jira jiraClient

	needImpactStatementRequest jiraItems
}

func gatherOptions() tea.Msg {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.jira.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		// TODO(muller): Something
	}

	if err := o.validate(); err != nil {
		// TODO(muller): Something
	}
	return optionsMsg(o)
}

func makeJiraClientCmd(o options) tea.Cmd {
	return func() tea.Msg {
		jc, err := o.jira.Client()
		if err != nil {
			// TODO(muller): Something
		}
		return jiraClientMsg(jc)
	}
}

func (m model) Init() tea.Cmd {
	return gatherOptions
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case optionsMsg:
		return m, makeJiraClientCmd(options(msg))
	case jiraClientMsg:
		m.jira = jiraClient(msg)
		return m, refreshNeedImpactStatementRequest(m.needImpactStatementRequest, m.jira)
	case needImpactStatementRequestMsg:
		m.needImpactStatementRequest = jiraItems(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.needImpactStatementRequest.table, cmd = m.needImpactStatementRequest.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return m.needImpactStatementRequest.View() + "\n\nPress 'q' to quit"
}

func main() {
	if _, err := tea.NewProgram(initialModel()).Run(); err != nil {
		fmt.Printf("There was an error: %v\n", err)
		os.Exit(1)
	}
}
