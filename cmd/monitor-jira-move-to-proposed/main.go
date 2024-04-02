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
	impactStatementRequested = "ImpactStatementRequested"
	impactStatementProposed  = "ImpactStatementProposed"
)

type options struct {
	bugId                      int
	impactStatementRequestCard string

	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.bugId, "bug", 0, "The numerical part of the OCPBUGS card to move to ImpactStatementProposed state")
	fs.StringVar(&o.impactStatementRequestCard, "impact-statement-card", "", "Full JIRA ID of the impact statement request card (optional)")

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
	// TODO(muller): Cobrify as ota monitor jira move-to-proposed(?)
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

	var impactStatementRequestCandidates []*jira.Issue
	for _, link := range blockerCandidate.Fields.IssueLinks {
		if outward := link.OutwardIssue; outward != nil && !strings.HasPrefix(outward.Key, "OCPBUGS-") && outward.Fields.Type.Name == "Spike" {
			logrus.Infof("%s is a potential impact statement request (%s %s %s)", outward.Key, ocpbugsId, link.Type.Outward, outward.Key)
			impactStatementRequestCandidates = append(impactStatementRequestCandidates, outward)
		}
		if inward := link.InwardIssue; inward != nil && !strings.HasPrefix(inward.Key, "OCPBUGS-") && inward.Fields.Type.Name == "Spike" {
			logrus.Infof("%s is a potential impact statement request (%s %s %s)", inward.Key, ocpbugsId, link.Type.Inward, inward.Key)
			impactStatementRequestCandidates = append(impactStatementRequestCandidates, inward)
		}
	}

	var impactStatementRequest *jira.Issue
	switch len(impactStatementRequestCandidates) {
	case 0:
		logrus.Warning("No impact statement requests found")
	case 1:
		impactStatementRequest = impactStatementRequestCandidates[0]
		logrus.Infof("Found a single impact statement request: %s %s", impactStatementRequest.Key, impactStatementRequest.Fields.Summary)
	default:
		logrus.Infof("Found multiple possible impact statement requests:")
		for _, candidate := range impactStatementRequestCandidates {
			fmt.Printf("  %s: %s", candidate.Key, candidate.Fields.Summary)
			if candidate.Key == o.impactStatementRequestCard {
				impactStatementRequest = candidate
				fmt.Printf(" (selected)")
			}
			fmt.Printf("\n")
		}
		if o.impactStatementRequestCard == "" {
			logrus.Infof("Rerun and pass the correct one with --impact-statement-card:")
		}
	}

	// logrus.Infof("Adding an informative comment to %s card", blockerCandidate.Key)
	// TODO(muller): Actually add a comment - but only if we actually change some state
	logrus.Infof("%s: Removing %s and adding %s", blockerCandidate.Key, impactStatementRequested, impactStatementProposed)
	labels := sets.New[string](blockerCandidate.Fields.Labels...).Delete(impactStatementRequested).Insert(impactStatementProposed)

	if _, err := jiraClient.UpdateIssue(&jira.Issue{
		Key:    blockerCandidate.Key,
		Fields: &jira.IssueFields{Labels: sets.List(labels)},
	}); err != nil {
		logrus.WithError(err).Fatal("cannot update issue")
	}

	// logrus.Infof("Adding an informative comment to %s card", ...)
	// TODO(muller): Actually add a comment - but only if we actually change some state
	if impactStatementRequest != nil {
		logrus.Infof("%s: Moving Impact Statement Request card to CODE REVIEW", impactStatementRequest.Key)
		if err := jiraClient.UpdateStatus(impactStatementRequest.Key, "CODE REVIEW"); err != nil {
			logrus.WithError(err).Fatal("failed to update impact statement request card status to CODE REVIEW")
		}
	}
}
