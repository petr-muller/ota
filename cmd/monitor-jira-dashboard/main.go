package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/flagutil"
)

const (
	jqlNeedImpactStatementRequest = "project = OCPBUGS AND labels in (UpgradeBlocker) AND labels not in (ImpactStatementRequested, ImpactStatementProposed, UpdateRecommendationsBlocked)"
	jqlNeedImpactStatement        = "project = OCPBUGS AND labels in (UpgradeBlocker) AND labels in (ImpactStatementRequested)"
	jqlHaveImpactStatement        = "project = OCPBUGS AND labels in (ImpactStatementProposed)"
)

type options struct {
	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	o.jira.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}

	return o
}

func (o *options) validate() error {
	return o.jira.Validate(false)
}

func main() {
	// TODO(muller): Cobrify as ota monitor dashboard
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	jiraClient, err := o.jira.Client()
	if err != nil {
		logrus.WithError(err).Fatal("cannot create Jira client")
	}

	now := time.Now()

	logrus.Infof("Obtaining JIRAs that need an impact statement request")
	needImpactStatementRequest, _, err := jiraClient.SearchWithContext(context.Background(), jqlNeedImpactStatementRequest, nil)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to query JIRA")
	}

	logrus.Infof("Obtaining JIRAs that wait for an impact statement")
	needImpactStatement, _, err := jiraClient.SearchWithContext(context.Background(), jqlNeedImpactStatement, nil)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to query JIRA")
	}

	logrus.Infof("Obtaining JIRAs that have a proposed impact statement")
	haveImpactStatement, _, err := jiraClient.SearchWithContext(context.Background(), jqlHaveImpactStatement, nil)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to query JIRA")
	}

	// TODO(muller): DRY the code
	// TODO(muller): Cache the results and emphasize items that changed since the last run
	// TODO(muller): Maybe show activity since last run somehow
	fmt.Printf("\n=== JIRAs that need an impact statement request ===\n\n")
	tabw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = tabw.Write([]byte("ID\tSUMMARY\tCOMPONENT\tMODIFIED\tAFFECTS\n"))
	for _, issue := range needImpactStatementRequest {
		id := issue.Key
		summary := issue.Fields.Summary
		component := issue.Fields.Components[0].Name
		sinceUpdated := now.Sub(time.Time(issue.Fields.Updated)).Truncate(time.Minute)
		var affects []string
		for _, version := range issue.Fields.AffectsVersions {
			affects = append(affects, version.Name)
		}
		_, _ = tabw.Write([]byte(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", id, summary, component, sinceUpdated.String(), strings.Join(affects, "|"))))
	}
	_ = tabw.Flush()

	// TODO(muller): Show impact statement card and whether it changed
	fmt.Printf("\n=== JIRAs that wait for a developer to provide an impact statement ===\n\n")
	tabw = tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = tabw.Write([]byte("ID\tSUMMARY\tCOMPONENT\tMODIFIED\tAFFECTS\n"))
	for _, issue := range needImpactStatement {
		id := issue.Key
		summary := issue.Fields.Summary
		component := issue.Fields.Components[0].Name
		sinceUpdated := now.Sub(time.Time(issue.Fields.Updated)).Truncate(time.Minute)
		var affects []string
		for _, version := range issue.Fields.AffectsVersions {
			affects = append(affects, version.Name)
		}
		_, _ = tabw.Write([]byte(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", id, summary, component, sinceUpdated.String(), strings.Join(affects, "|"))))
	}
	_ = tabw.Flush()

	fmt.Printf("\n=== JIRAs where a developer proposed an impact statement ===\n\n")
	tabw = tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = tabw.Write([]byte("ID\tSUMMARY\tCOMPONENT\tMODIFIED\tAFFECTS\n"))
	for _, issue := range haveImpactStatement {
		id := issue.Key
		summary := issue.Fields.Summary
		component := issue.Fields.Components[0].Name
		sinceUpdated := now.Sub(time.Time(issue.Fields.Updated)).Truncate(time.Minute)
		var affects []string
		for _, version := range issue.Fields.AffectsVersions {
			affects = append(affects, version.Name)
		}
		_, _ = tabw.Write([]byte(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", id, summary, component, sinceUpdated.String(), strings.Join(affects, "|"))))
	}
	_ = tabw.Flush()
}
