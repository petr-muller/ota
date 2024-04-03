package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"

	"github.com/petr-muller/ota/internal/updateblockers"
)

type options struct {
	bugId                      int
	impactStatementRequestCard string
	riskName                   string

	graphRepositoryPath string

	jira flagutil.JiraOptions
}

type PromQLQuery struct {
	Query string `yaml:"promql"`
}

type PromQLRule struct {
	Type   string      `yaml:"type"`
	PromQL PromQLQuery `yaml:"promql"`
}

type ConditionallyBlockedEdge struct {
	To            string       `yaml:"to"`
	From          string       `yaml:"from"`
	FixedIn       string       `yaml:"fixedIn,omitempty"`
	URL           string       `yaml:"url"`
	Name          string       `yaml:"name"`
	Message       string       `yaml:"message"`
	MatchingRules []PromQLRule `yaml:"matchingRules"`
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.bugId, "bug", 0, "The numerical part of the OCPBUGS card to move to ImpactStatementProposed state")
	fs.StringVar(&o.impactStatementRequestCard, "impact-statement-card", "", "Full JIRA ID of the impact statement request card (optional)")
	fs.StringVar(&o.riskName, "risk", "", "The name of the conditional risk that was set up")

	fs.StringVar(&o.graphRepositoryPath, "graph-repository-path", "", "The path to the Cincinnati graph repository")

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

	if o.graphRepositoryPath == "" {
		return fmt.Errorf("--graph-repository-path must be specified and nonempty")
	}

	return o.jira.Validate(false)
}

func main() {
	// TODO(muller): Cobrify as ota monitor jira move-to-updaterecommendationblocked(?)
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

	var conditionalRiskName string
	var conditionalRiskSummary string

	logrus.Infof("%s: Removing %s,%s (if present) and adding %s,%s", blockerCandidate.Key, updateblockers.LabelImpactStatementRequested, updateblockers.LabelImpactStatementProposed, updateblockers.LabelKnownIssueAnnounced, updateblockers.LabelBlocker)
	labels := sets.New[string](blockerCandidate.Fields.Labels...).Delete(updateblockers.LabelImpactStatementRequested, updateblockers.LabelImpactStatementProposed).Insert(updateblockers.LabelKnownIssueAnnounced, updateblockers.LabelBlocker)

	if _, err := jiraClient.UpdateIssue(&jira.Issue{
		Key:    blockerCandidate.Key,
		Fields: &jira.IssueFields{Labels: sets.List(labels)},
	}); err != nil {
		logrus.WithError(err).Fatal("cannot update issue")
	}

	if impactStatementRequest != nil {
		logrus.Infof("%s: Labelling Impact Statement Request card with %s for searchability", impactStatementRequest.Key, updateblockers.LabelBlocker)
		labels := sets.New[string](impactStatementRequest.Fields.Labels...).Insert(updateblockers.LabelBlocker)
		if _, err := jiraClient.UpdateIssue(&jira.Issue{
			Key:    impactStatementRequest.Key,
			Fields: &jira.IssueFields{Labels: sets.List(labels)},
		}); err != nil {
			logrus.WithError(err).Fatal("cannot update issue")
		}

		logrus.Infof("%s: Moving Impact Statement Request card to CLOSED", impactStatementRequest.Key)
		if err := jiraClient.UpdateStatus(impactStatementRequest.Key, "CLOSED"); err != nil {
			logrus.WithError(err).Fatal("failed to update impact statement request card status to CLOSED")
		}

		// TODO: Maybe just query OSUS instead of looking into data on disk?
		logrus.Infof("Looking for conditional risk that links to %s", impactStatementRequest.Key)
		edgesDirectory := filepath.Join(o.graphRepositoryPath, "blocked-edges")
		if err := filepath.WalkDir(edgesDirectory, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				logrus.WithError(err).Error("Failure when walking items in graph repository directory %s", edgesDirectory)
				return err
			}

			if conditionalRiskName != "" {
				return nil
			}

			if d.IsDir() {
				logrus.Trace("Skipping (unexpected) directory %s", path)
				return nil
			}

			edgeRaw, err := os.ReadFile(path)
			if err != nil {
				logrus.WithError(err).Error("Cannot read target file %s", path)
				return err
			}

			var edge ConditionallyBlockedEdge
			if err := yaml.Unmarshal(edgeRaw, &edge); err != nil {
				logrus.WithError(err).Error("Cannot unmarshal target file %s", path)
				return err
			}

			if edge.URL == fmt.Sprintf("https://issues.redhat.com/browse/%s", impactStatementRequest.Key) {
				conditionalRiskName = edge.Name
				conditionalRiskSummary = edge.Message
			}

			return nil
		}); err != nil {
			logrus.WithError(err).Fatal("cannot walk graph repository")
		}

		bugCommentBody := fmt.Sprintf(`Based on the impact assessment %s, known issue / conditional risk for this bug was added to the update graph. {{%s}}, {{%s}} labels were added to this card. {{%s}}, {{%s}}, labels were removed if they were present.

Details of the conditional risk:

* *Name:* {{%s}}
* *Summary:* %s`,
			impactStatementRequest.Key,
			updateblockers.LabelKnownIssueAnnounced, updateblockers.LabelBlocker, updateblockers.LabelImpactStatementRequested, updateblockers.LabelImpactStatementProposed,
			conditionalRiskName, conditionalRiskSummary)

		bugComment := &jira.Comment{
			Author: jira.User{
				Name: "afri@afri.cz", // TODO(muller): Use the user associated with the Jira client
			},
			Body:       bugCommentBody,
			Visibility: jira.CommentVisibility{}, // TODO(muller): Use employee visibility
		}

		logrus.Infof("%s: Adding an informative comment to bug card", blockerCandidate.Key)
		if _, err := jiraClient.AddComment(blockerCandidate.ID, bugComment); err != nil {
			logrus.WithError(err).Fatal("cannot create comment")
		}

		isrCommentBody := fmt.Sprintf(`Based on the impact assessment, known issue / conditional risk for this bug was added to the update graph. {{%s}} label was added to this card for searchability.

This card has been closed. _Note this does not mean the bug is resolved, only that its impact is understood enough for setting up a conditional risk in the update graph. Please refer to %s and its clones for information about fix state in particular versions._

----

Details of the conditional risk:

* *Name:* {{%s}}
* *Summary:* %s`,
			updateblockers.LabelBlocker, blockerCandidate.Key, conditionalRiskName, conditionalRiskSummary)

		isrComment := &jira.Comment{
			Author: jira.User{
				Name: "afri@afri.cz", // TODO(muller): Use the user associated with the Jira client
			},
			Body:       isrCommentBody,
			Visibility: jira.CommentVisibility{}, // TODO(muller): Use employee visibility
		}

		logrus.Infof("%s: Adding an informative comment to impact statement card", impactStatementRequest.Key)
		if _, err := jiraClient.AddComment(impactStatementRequest.ID, isrComment); err != nil {
			logrus.WithError(err).Fatal("cannot create comment")
		}
	}

}
