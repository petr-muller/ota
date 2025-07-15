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
	bearerTokenFileRef *string
	endpointRef        *string
}

// AddFlags injects Jira options into the given FlagSet
func (o *JiraOptions) AddFlags(fs *flag.FlagSet) {
	configDir := config.MustOtaConfigDir()
	defaultTokenPath := filepath.Join(configDir, tokenFileName)

	o.JiraOptions.AddCustomizedFlags(fs,
		prowflagutil.JiraDefaultEndpoint("https://issues.redhat.com"),
		prowflagutil.JiraDefaultBearerTokenFile(defaultTokenPath),
		prowflagutil.JiraNoBasicAuth(),
	)
}

// AddPFlags injects Jira options into the given pflag.FlagSet
func (o *JiraOptions) AddPFlags(fs *pflag.FlagSet) {
	configDir := config.MustOtaConfigDir()
	defaultTokenPath := filepath.Join(configDir, tokenFileName)

	// Use pflag to add the flags and bind them manually
	var bearerTokenFile, endpoint string
	fs.StringVar(&bearerTokenFile, "jira.bearer-token-file", defaultTokenPath, "Path to the file containing the Jira bearer token")
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
	o.bearerTokenFileRef = &bearerTokenFile
	o.endpointRef = &endpoint
}

// SetFromPFlags copies values from pflag variables to the JiraOptions
func (o *JiraOptions) SetFromPFlags() {
	if o.bearerTokenFileRef != nil {
		goFlags := flag.NewFlagSet("temp", flag.ContinueOnError)
		o.JiraOptions.AddCustomizedFlags(goFlags,
			prowflagutil.JiraDefaultEndpoint(*o.endpointRef),
			prowflagutil.JiraDefaultBearerTokenFile(*o.bearerTokenFileRef),
			prowflagutil.JiraNoBasicAuth(),
		)
		goFlags.Parse([]string{}) // Parse empty args to set defaults
	}
}

func (o *JiraOptions) Validate() error {
	return o.JiraOptions.Validate(false)
}
