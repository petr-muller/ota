package flagutil

import (
	"flag"

	"github.com/spf13/pflag"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
)

const (
	defaultJiraEndpoint = "https://redhat.atlassian.net"
)

func JiraEndpoint() string {
	return defaultJiraEndpoint
}

type JiraOptions struct {
	prowflagutil.JiraOptions
	endpointRef *string
}

// AddFlags injects Jira options into the given FlagSet
func (o *JiraOptions) AddFlags(fs *flag.FlagSet) {
	o.JiraOptions.AddCustomizedFlags(fs,
		prowflagutil.JiraDefaultEndpoint(defaultJiraEndpoint),
	)
}

// AddPFlags injects Jira options into the given pflag.FlagSet
func (o *JiraOptions) AddPFlags(fs *pflag.FlagSet) {
	// Use pflag to add the flags and bind them manually
	var endpoint string
	fs.StringVar(&endpoint, "jira.endpoint", defaultJiraEndpoint, "Jira endpoint URL")

	// Set up a hook to copy values after parsing
	fs.SetNormalizeFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "jira.endpoint":
			// We'll handle this in a pre-run hook
		}
		return pflag.NormalizedName(name)
	})

	o.endpointRef = &endpoint
}

// SetFromPFlags copies values from pflag variables to the JiraOptions
func (o *JiraOptions) SetFromPFlags() {
	goFlags := flag.NewFlagSet("temp", flag.ContinueOnError)
	o.JiraOptions.AddCustomizedFlags(goFlags,
		prowflagutil.JiraDefaultEndpoint(*o.endpointRef),
	)
	goFlags.Parse([]string{}) // Parse empty args to set defaults
}

func (o *JiraOptions) Validate() error {
	return o.JiraOptions.Validate(false)
}
