package main

import (
	"context"
	"fmt"

	"github.com/charmbracelet/fang"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/petr-muller/ota/internal/flagutil"
	"github.com/petr-muller/ota/internal/jirawatch/service"
	"github.com/petr-muller/ota/internal/jirawatch/storage"
	"github.com/petr-muller/ota/internal/jirawatch/ui"
)

var (
	jiraOptions    flagutil.JiraOptions
	queryName      string
	queryJQL       string
	queryDescription string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "jira-query-watch",
		Short: "Watch changes in JIRA issues matching a JQL query",
		Long: `JIRA Query Watcher allows you to watch changes in JIRA issues matching a JQL query over time.
It provides three modes of operation:

1. Create/Update query: Store a new query or update an existing one
2. Inspect query: View the current state of a stored query
3. List queries: Show all stored queries`,
	}

	// Add global flags
	jiraOptions.AddPFlags(rootCmd.PersistentFlags())

	// Add subcommands
	rootCmd.AddCommand(
		newWatchCmd(),
		newInspectCmd(),
		newListCmd(),
		newDeleteCmd(),
	)

	// Use fang to execute the command
	if err := fang.Execute(context.Background(), rootCmd); err != nil {
		logrus.WithError(err).Fatal("command failed")
	}
}

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch <query-name> <jql-query>",
		Short: "Create or update a query to watch",
		Long: `Create a new query to watch or update an existing one.
The matching JIRA issue information will be fetched, displayed, and stored for future reference.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			queryName = args[0]
			queryJQL = args[1]
			return runWatch(cmd.Context())
		},
	}

	cmd.Flags().StringVarP(&queryDescription, "description", "d", "", "Optional description for the query")

	return cmd
}

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <query-name>",
		Short: "Inspect the current state of a stored query",
		Long: `Inspect the current state of JIRA issues matching an existing query.
The matching JIRA issue information will be fetched, displayed, and stored for future reference.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			queryName = args[0]
			return runInspect(cmd.Context())
		},
	}

	return cmd
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all stored queries",
		Long:  `List all stored queries by name.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}

	return cmd
}

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <query-name>",
		Short: "Delete a stored query",
		Long:  `Delete a stored query by name.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			queryName = args[0]
			return runDelete()
		},
	}

	return cmd
}

func createService() (*service.Service, error) {
	// Copy pflag values to JiraOptions
	jiraOptions.SetFromPFlags()

	if err := jiraOptions.Validate(); err != nil {
		return nil, fmt.Errorf("invalid JIRA options: %w", err)
	}

	dataDir, err := storage.JiraWatchDataDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine data directory: %w", err)
	}

	svc, err := service.NewService(jiraOptions, dataDir)
	if err != nil {
		return nil, fmt.Errorf("cannot create service: %w", err)
	}

	return svc, nil
}

func runWatch(ctx context.Context) error {
	svc, err := createService()
	if err != nil {
		return err
	}

	opts := service.WatchQueryOptions{
		Name:        queryName,
		JQL:         queryJQL,
		Description: queryDescription,
	}

	result, err := svc.WatchQuery(ctx, opts)
	if err != nil {
		return fmt.Errorf("cannot watch query: %w", err)
	}

	if len(result.Query.Issues) == 0 {
		fmt.Printf("No issues found matching query '%s'\n", queryName)
		return nil
	}

	model := ui.NewModel(queryName, *result, result.Query.LastFetched)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("cannot run TUI: %w", err)
	}

	return nil
}

func runInspect(ctx context.Context) error {
	svc, err := createService()
	if err != nil {
		return err
	}

	if !svc.QueryExists(queryName) {
		return fmt.Errorf("query '%s' not found", queryName)
	}

	result, err := svc.InspectQuery(ctx, queryName)
	if err != nil {
		return fmt.Errorf("cannot inspect query: %w", err)
	}

	if len(result.Query.Issues) == 0 {
		fmt.Printf("No issues found matching query '%s'\n", queryName)
		return nil
	}

	model := ui.NewModel(queryName, *result, result.Query.LastFetched)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("cannot run TUI: %w", err)
	}

	return nil
}

func runList() error {
	svc, err := createService()
	if err != nil {
		return err
	}

	queries, err := svc.ListQueriesDetailed()
	if err != nil {
		return fmt.Errorf("cannot list queries: %w", err)
	}

	if len(queries) == 0 {
		fmt.Println("No stored queries found")
		return nil
	}

	fmt.Println("Stored queries:")
	for _, query := range queries {
		fmt.Printf("  - %s", query.Name)
		if query.Description != "" {
			fmt.Printf(" - %s", query.Description)
		}
		fmt.Printf(" (%d issues", query.IssueCount)
		if !query.LastFetched.IsZero() {
			fmt.Printf(", last fetched: %s", query.LastFetched.Format("2006-01-02 15:04"))
		}
		fmt.Printf(")\n")
	}

	return nil
}

func runDelete() error {
	svc, err := createService()
	if err != nil {
		return err
	}

	if !svc.QueryExists(queryName) {
		return fmt.Errorf("query '%s' not found", queryName)
	}

	if err := svc.DeleteQuery(queryName); err != nil {
		return fmt.Errorf("cannot delete query: %w", err)
	}

	fmt.Printf("Query '%s' deleted successfully\n", queryName)
	return nil
}