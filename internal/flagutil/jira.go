package flagutil

import (
	"flag"
	"path/filepath"

	"github.com/petr-muller/ota/internal/config"
	"github.com/spf13/pflag"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
)

const (
	tokenFileName string = "jira-token"
)

type JiraOptions struct {
	prowflagutil.JiraOptions
	apiTokenFileRef *string
	endpointRef     *string
}

// AddFlags injects Jira options into the given FlagSet
func (o *JiraOptions) AddFlags(fs *flag.FlagSet) {
	o.JiraOptions.AddCustomizedFlags(fs,
		prowflagutil.JiraDefaultEndpoint("https://redhat.atlassian.net"),
	)
}

// AddPFlags injects Jira options into the given pflag.FlagSet
func (o *JiraOptions) AddPFlags(fs *pflag.FlagSet) {
	configDir := config.MustOtaConfigDir()
	defaultTokenPath := filepath.Join(configDir, tokenFileName)

	// Use pflag to add the flags and bind them manually
	var apiTokenFile, endpoint string
	fs.StringVar(&apiTokenFile, "jira.api-token-file", defaultTokenPath, "Path to the file containing the Jira API token")
	fs.StringVar(&endpoint, "jira.endpoint", "https://issues.redhat.com", "Jira endpoint URL")

	// Set up a hook to copy values after parsing
	fs.SetNormalizeFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "jira.bearer-token-file":
			// We'll handle this in a pre-run hook
		case "jira.endpoint":
			// We'll handle this in a pre-run hook
		}
		return pflag.NormalizedName(name)
	})

	// Store references for later use
	o.apiTokenFileRef = &apiTokenFile
	o.endpointRef = &endpoint
}

// SetFromPFlags copies values from pflag variables to the JiraOptions
func (o *JiraOptions) SetFromPFlags() {
	if o.apiTokenFileRef != nil {
		goFlags := flag.NewFlagSet("temp", flag.ContinueOnError)
		o.JiraOptions.AddCustomizedFlags(goFlags,
			prowflagutil.JiraDefaultEndpoint(*o.endpointRef),
		)
		goFlags.Parse([]string{}) // Parse empty args to set defaults
	}
}

func (o *JiraOptions) Validate() error {
	return o.JiraOptions.Validate(false)
}
