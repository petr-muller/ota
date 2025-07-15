# JIRA Query Watcher (POC)

Build a simple tool called `jira-query-watch` that allows the user to watch changes in JIRA issues
matching a JQL query in time. It will be used in three modes:

1. Create a new query to watch: The user passes a name of the query and a JQL query string as an
   input. The tool will query the stored queries by the passed name, and if it does not exist, it
   means the new watched query is established; The matching JIRA issue information will be fetched,
   displayed to the user and stored for future reference (see the respective sections)
2. Update an existing query: The user passes a name of the query and a JQL query string as an
   input. The tool will query the stored queries by the passed name, and if it exists, it means the
   watched query is updated; The matching JIRA issue information will be fetched with the new query
   displayed, and stored for future reference (see the respective sections)
3. Inspect the current state of JIRA cards matching an existing query: The user passes a name of
   the query, and the tool will query the stored queries by the passed name, and if it exists, it
   means the watched query is inspected; The matching JIRA issue information will be fetched, and
   displayed to the user and stored for future reference (see the respective sections).

What JIRA issue information should be fetched: key, summary, component, status, last updated, labels and assignee.

## Storage

The tool will use a YAML storage file to store the watched queries. There will be one file per query
identified by name. The file will store what JQL query was used to fetch the issues, when was the
last time the issues were fetched, and the list of issues with their information. The files will be
stored in a directory called `jira-queries` in the users persistent program data directory for `ota`
(so $XDG_DATA_HOME/ota/jira-queries on Linux and ~/Library/Application Support/ota on MacOs).

## Display

The tool will display the fetched issues in a table format with the following columns: key, summary,
component, status, last updated, labels and assignee. The table should be displayed in a way that
the user can easily read it in the terminal. The table should be sorted by last updated date in
descending order. If there are no issues matching the query, the tool should display a message.

The tool should highlight the properties that have changed since the last time the issues were
fetched, and it should show that time (something like "changes since ...). It should highlight
the cards that newly appeared in the query results since the last time the issues were fetched.

The table should also display, in the same table, the cards that were removed from the query results
since the last time the issues were fetched. These cards should be displayed at the bottom of the
table with a different color (e.g. grey) to indicate that they were removed.

## Visuals

The tool should use the `charmbracelet/bubbletea` package for nice visuals.

## Implementation

The tool is a proof of concept for this functionality so all logic should be implemented in small
focused packages under internal and the code under `cmd` should be a fairly thin wrapper over library
functionality. The visual and interactive elements should be separate from the logic of fetching and
storing data.

The tool should be implemented in Go and should use the `go-jira` package to interact with JIRA. It
should be a cobra command line tool. The existing tools already have code for interacting with JIRA
(specifically where to find the JIRA server URL and how to authenticate), so that code should be reused.
