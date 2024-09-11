package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/petr-muller/ota/internal/updateblockers"
)

type options struct {
	bugId            int
	componentProject string // TODO(muller): Infer automatically

	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.bugId, "bug", 0, "The numerical part of the OCPBUGS card to create the impact statement request for")
	fs.StringVar(&o.componentProject, "for", "", "The project of the component to create the impact statement request for")

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

	if o.componentProject == "" {
		return fmt.Errorf("--for must be specified and nonempty")
	}

	return o.jira.Validate(false)
}

func main() {
	// TODO(muller): Cobrify as ota monitor jira create-impact-statement-request
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

	// TODO(muller): Validate whether it is a valid recipient for the impact statement request (labels, existence of impact statement, etc.)

	assignee := blockerCandidate.Fields.Assignee
	if assignee == nil {
		logrus.Warning("Issue %s has no assignee", ocpbugsId)
	} else {
		logrus.Infof("Issue %s is assigned to %s", ocpbugsId, assignee.Name)
	}

	impactStatementRequest := jira.Issue{
		Fields: &jira.IssueFields{
			Type:        jira.IssueType{Name: "Spike"},
			Project:     jira.Project{Key: o.componentProject},
			Priority:    &jira.Priority{Name: "Critical"},
			Labels:      []string{updateblockers.LabelBlocker},
			Description: fmt.Sprintf(descriptionTemplate, ocpbugsId, ocpbugsId),
			Summary:     fmt.Sprintf("Impact statement request for %s %s", ocpbugsId, blockerCandidate.Fields.Summary),
			Reporter:    &jira.User{Name: "afri@afri.cz"}, // TODO(muller): Use the user associated with the Jira client
		},
	}
	if assignee != nil {
		impactStatementRequest.Fields.Assignee = assignee
	}

	logrus.Infof("Creating impact statement request Spike card in %s project", o.componentProject)
	isrIssue, err := jiraClient.CreateIssue(&impactStatementRequest)
	if err != nil {
		logrus.WithError(err).Fatal("cannot create impact statement request")
	}

	logrus.Infof("Creating a '%s blocks %s' link between the cards", isrIssue.Key, blockerCandidate.Key)
	blockLink := jira.IssueLink{
		OutwardIssue: &jira.Issue{ID: blockerCandidate.ID},
		InwardIssue:  &jira.Issue{ID: isrIssue.ID},
		Type: jira.IssueLinkType{
			Name:    "Blocks",
			Inward:  "is blocked by",
			Outward: "blocks",
		},
	}

	if err := jiraClient.CreateIssueLink(&blockLink); err != nil {
		logrus.WithError(err).Fatal("cannot create issue link")
	}

	logrus.Infof("Adding an informative comment to %s card", blockerCandidate.Key)
	var assigneeComment string
	if assignee != nil {
		assigneeComment = fmt.Sprintf(" and assigned it to [~%s] (this card's assignee)", assignee.Name)
	}
	commentBody := fmt.Sprintf(
		"This card has been labeled as a potential upgrade risk with an {{UpgradeBlock}} label. We have created a card %s to help us understand the impact of the bug so that we can warn exposed cluster owners about it before they upgrade to an affected OCP version%s. The card simply asks for answers to several questions and should not require too much time to answer.",
		isrIssue.Key, assigneeComment,
	)

	candidateBugComment := &jira.Comment{
		Author: jira.User{
			Name: "afri@afri.cz", // TODO(muller): Use the user associated with the Jira client
		},
		Body:       commentBody,
		Visibility: jira.CommentVisibility{}, // TODO(muller): Use employee visibility
	}

	if _, err := jiraClient.AddComment(blockerCandidate.ID, candidateBugComment); err != nil {
		logrus.WithError(err).Fatal("cannot create comment")
	}

	logrus.Infof("Adding the ImpactStatementRequested label to %s card", blockerCandidate.Key)

	labels := sets.New[string](blockerCandidate.Fields.Labels...)
	labels.Insert(updateblockers.LabelImpactStatementRequested)
	labels.Insert(updateblockers.LabelBlocker)

	if _, err := jiraClient.UpdateIssue(&jira.Issue{
		Key:    blockerCandidate.Key,
		Fields: &jira.IssueFields{Labels: sets.List(labels)},
	}); err != nil {
		logrus.WithError(err).Fatal("cannot update issue")
	}

}

var descriptionTemplate = `We're asking the following questions to evaluate whether or not %s warrants changing update recommendations from either the previous X.Y or X.Y.Z. The ultimate goal is to avoid recommending an update which introduces new risk or reduces cluster functionality in any way. In the absence of a declared update risk (the status quo), there is some risk that the existing fleet updates into the at-risk releases. Depending on the bug and estimated risk, leaving the update risk undeclared may be acceptable.

Sample answers are provided to give more context and the {{ImpactStatementRequested}} label has been added to %s. When responding, please move this ticket to {{{}Code Review{}}}. The expectation is that the assignee answers these questions.

h2. Which 4.y.z to 4.y'.z' updates increase vulnerability?
 * reasoning: This allows us to populate [{{from}} and {{to}} in conditional update recommendations|https://github.com/openshift/cincinnati-graph-data/tree/0335e56cde6b17230106f137382cbbd9aa5038ed#block-edges] for "the {{$SOURCE_RELEASE}} to {{$TARGET_RELEASE}} update is exposed.
 * example: Customers upgrading from any 4.y (or specific 4.y.z) to 4.(y+1).z'. Use {{oc adm upgrade}} to show your current cluster version.

h2. Which types of clusters?
 * reasoning: This allows us to populate [{{matchingRules}} in conditional update recommendations|https://github.com/openshift/cincinnati-graph-data/tree/0335e56cde6b17230106f137382cbbd9aa5038ed#block-edges] for "clusters like {{{}$THIS{}}}".
 * example: GCP clusters with thousands of namespaces, approximately 5%% of the subscribed fleet. Check your vulnerability with {{oc ...}} or the following PromQL {{{}count (...) > 0{}}}.

The two questions above are sufficient to declare an initial update risk, and we would like as much detail as possible on them as quickly as you can get it. Perfectly crisp responses are nice, but are not required. For example "it seems like these platforms are involved, because..." in a day 1 draft impact statement is helpful, even if you follow up with "actually, it was these other platforms" on day 3. In the absence of a response within 7 days, we may or may not declare a conditional update risk based on our current understanding of the issue.

If you can, answers to the following questions will make the conditional risk declaration more actionable for customers.

h2. What is the impact? Is it serious enough to warrant removing update recommendations?
 * reasoning: This allows us to populate [{{name}} and {{message}} in conditional update recommendations|https://github.com/openshift/cincinnati-graph-data/tree/0335e56cde6b17230106f137382cbbd9aa5038ed#block-edges] for "...because if you update, {{$THESE_CONDITIONS}} may cause {{{}$THESE_UNFORTUNATE_SYMPTOMS{}}}".
 * example: Around 2 minute disruption in edge routing for 10%% of clusters. Check with {{{}oc ...{}}}.
 * example: Up to 90 seconds of API downtime. Check with {{{}curl ...{}}}.
 * example: etcd loses quorum and you have to restore from backup. Check with {{{}ssh ...{}}}.

h2. How involved is remediation?
 * reasoning: This allows administrators who are already vulnerable, or who chose to waive conditional-update risks, to recover their cluster. And even moderately serious impacts might be acceptable if they are easy to mitigate.
 * example: Issue resolves itself after five minutes.
 * example: Admin can run a single: {{{}oc ...{}}}.
 * example: Admin must SSH to hosts, restore from backups, or other non standard admin activities.

h2. Is this a regression?
 * reasoning: Updating between two vulnerable releases may not increase exposure (unless rebooting during the update increases vulnerability, etc.). We only qualify update recommendations if the update increases exposure.
 * example: No, it has always been like this we just never noticed.
 * example: Yes, from 4.y.z to 4.y+1.z Or 4.y.z to 4.y.z+1.`
