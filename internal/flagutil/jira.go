package flagutil

import (
	"flag"
	"path/filepath"

	"github.com/petr-muller/ota/internal/config"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
)

const (
	tokenFileName string = "jira-token"
)

type JiraOptions struct {
	prowflagutil.JiraOptions
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

func (o *JiraOptions) Validate() error {
	return o.JiraOptions.Validate(false)
}
