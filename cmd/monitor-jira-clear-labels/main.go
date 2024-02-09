package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
)

const (
	upgradeBlockerCandidate  = "UpgradeBlocker"
	impactStatementRequested = "ImpactStatementRequested"
	impactStatementProposed  = "ImpactStatementProposed"
	knownIssueAnnounced      = "UpgradeRecommendationBlocked"
)

type options struct {
	bugId int

	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.bugId, "bug", 0, "The numerical part of the OCPBUGS card to create the impact statement request for")

	o.jira.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}

	return o
}

func (o *options) validate() error {
	if o.bugId == 0 {
		return fmt.Errorf("--bug must be specified and nonzero")
	}

	return o.jira.Validate(false)
}

func main() {
	// TODO(muller): Cobrify as ota monitor jira clear-upgradeblocker-labels
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	jiraClient, err := o.jira.Client()
	if err != nil {
		logrus.WithError(err).Fatal("cannot create Jira client")
	}

	ocpbugsId := fmt.Sprintf("OCPBUGS-%d", o.bugId)
	logrus.Infof("Obtaining issue %s", ocpbugsId)

	blockerCandidate, err := jiraClient.GetIssue(ocpbugsId)
	if err != nil {
		logrus.WithError(err).Fatal("cannot get issue")
	}

	// logrus.Infof("Adding an informative comment to %s card", blockerCandidate.Key)
	// TODO(muller): Actually add a comment

	toRemove := sets.New(upgradeBlockerCandidate, impactStatementRequested, impactStatementProposed, knownIssueAnnounced)

	logrus.Infof("Clearing OTA labels (%s) from %s card", strings.Join(sets.List(toRemove), ","), blockerCandidate.Key)
	labels := sets.New[string](blockerCandidate.Fields.Labels...).Difference(toRemove)

	if _, err := jiraClient.UpdateIssue(&jira.Issue{
		Key:    blockerCandidate.Key,
		Fields: &jira.IssueFields{Labels: sets.List(labels)},
	}); err != nil {
		logrus.WithError(err).Fatal("cannot update issue")
	}
}
