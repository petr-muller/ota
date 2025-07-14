package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/petr-muller/ota/internal/flagutil"
	"github.com/petr-muller/ota/internal/mappings"
	"github.com/petr-muller/ota/internal/updateblockers"
)

var validTaskTypes = []string{"Spike", "Task"}

type options struct {
	bugId            int
	componentProject string // TODO(muller): Infer automatically
	taskType         string

	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.bugId, "bug", 0, "The numerical part of the OCPBUGS card to create the impact statement request for")
	fs.StringVar(&o.componentProject, "for", "", "The project of the component to create the impact statement request for")
	fs.StringVar(&o.taskType, "type", validTaskTypes[0], fmt.Sprintf("The type of Jira issue to create (%s)", strings.Join(validTaskTypes, " or ")))

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

	// componentProject can be empty if we can derive it from mappings

	validType := false
	for _, validTaskType := range validTaskTypes {
		if o.taskType == validTaskType {
			validType = true
			break
		}
	}
	if !validType {
		return fmt.Errorf("--type must be one of (%s), got '%s'", strings.Join(validTaskTypes, ", "), o.taskType)
	}

	return o.jira.Validate()
}

func getComponentName(issue *jira.Issue) (string, error) {
	if len(issue.Fields.Components) == 0 {
		return "", fmt.Errorf("issue %s has no components", issue.Key)
	}

	if len(issue.Fields.Components) > 1 {
		logrus.Warnf("Issue %s has multiple components, using the first one: %s", issue.Key, issue.Fields.Components[0].Name)
	}

	return issue.Fields.Components[0].Name, nil
}

func askForConfirmation(message string) bool {
	fmt.Printf("%s (y/N): ", message)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		logrus.WithError(err).Warn("Failed to read user input, defaulting to 'no'")
		return false
	}
	
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

func determineProject(componentName, providedProject string, m *mappings.Mappings) (string, error) {
	// If no component, must have provided project
	if componentName == "" {
		if providedProject == "" {
			return "", fmt.Errorf("could not determine component and --for not provided")
		}
		return providedProject, nil
	}
	
	mappedProject := m.GetProjectForComponent(componentName)
	
	// No --for provided, use mapping if available
	if providedProject == "" {
		if mappedProject == "" {
			return "", fmt.Errorf("no mapping found for component %s and --for not provided", componentName)
		}
		logrus.Infof("Using mapped project %s for component %s", mappedProject, componentName)
		return mappedProject, nil
	}
	
	// --for was provided, check for conflicts
	if mappedProject == "" || mappedProject == providedProject {
		return providedProject, nil
	}
	
	// Conflict: ask user which to use
	if askForConfirmation(fmt.Sprintf("Component %s is mapped to project %s, but you provided %s. Use provided value?", componentName, mappedProject, providedProject)) {
		logrus.Infof("Using provided project %s instead of mapped %s", providedProject, mappedProject)
		return providedProject, nil
	}
	
	logrus.Infof("Using mapped project %s instead of provided %s", mappedProject, providedProject)
	return mappedProject, nil
}

func determineTaskType(project, providedTaskType string, m *mappings.Mappings) string {
	mappedTaskType := m.GetTaskTypeForProject(project)
	
	// No mapping or same as provided
	if mappedTaskType == "" || mappedTaskType == providedTaskType {
		return providedTaskType
	}
	
	// Use mapped type only if provided type is default
	if providedTaskType == validTaskTypes[0] {
		logrus.Infof("Using mapped task type %s for project %s", mappedTaskType, project)
		return mappedTaskType
	}
	
	logrus.Infof("Provided task type %s overrides mapped task type %s for project %s", providedTaskType, mappedTaskType, project)
	return providedTaskType
}

func saveComponentMappingIfNeeded(componentName, providedProject, finalProject string, m *mappings.Mappings) {
	if componentName == "" || providedProject == "" {
		return
	}
	
	mappedProject := m.GetProjectForComponent(componentName)
	
	// New mapping
	if mappedProject == "" {
		m.SetComponentMapping(componentName, finalProject)
		logrus.Infof("Saved new component mapping: %s -> %s", componentName, finalProject)
		return
	}
	
	// Mapping unchanged
	if mappedProject == finalProject {
		return
	}
	
	// User chose to override, ask if they want to update mapping
	if askForConfirmation(fmt.Sprintf("Update mapping for component %s from %s to %s?", componentName, mappedProject, finalProject)) {
		m.SetComponentMapping(componentName, finalProject)
		logrus.Infof("Updated component mapping: %s -> %s", componentName, finalProject)
	}
}

func saveTaskTypeMappingIfNeeded(project, finalTaskType string, m *mappings.Mappings) {
	// Only save non-default task types
	if finalTaskType == validTaskTypes[0] {
		return
	}
	
	mappedTaskType := m.GetTaskTypeForProject(project)
	if mappedTaskType != "" {
		return
	}
	
	m.SetTaskTypeMapping(project, finalTaskType)
	logrus.Infof("Saved new task type mapping: %s -> %s", project, finalTaskType)
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

	// Load mappings
	m, err := mappings.LoadMappings()
	if err != nil {
		logrus.WithError(err).Fatal("cannot load mappings")
	}

	ocpbugsId := fmt.Sprintf("OCPBUGS-%d", o.bugId)
	logrus.Infof("Obtaining issue %s", ocpbugsId)

	blockerCandidate, err := jiraClient.GetIssue(ocpbugsId)
	if err != nil {
		logrus.WithError(err).Fatal("cannot get issue")
	}

	// Extract component name from the issue
	componentName, err := getComponentName(blockerCandidate)
	if err != nil {
		logrus.WithError(err).Warnf("Could not determine component for %s", ocpbugsId)
		componentName = ""
	} else {
		logrus.Infof("Issue %s has component: %s", ocpbugsId, componentName)
	}

	// Determine the project and task type to use
	finalProject, err := determineProject(componentName, o.componentProject, m)
	if err != nil {
		logrus.WithError(err).Fatal("cannot determine project")
	}

	finalTaskType := determineTaskType(finalProject, o.taskType, m)

	// TODO(muller): Validate whether it is a valid recipient for the impact statement request (labels, existence of impact statement, etc.)

	assignee := blockerCandidate.Fields.Assignee
	if assignee == nil {
		logrus.Warnf("Issue %s has no assignee", ocpbugsId)
	} else {
		logrus.Infof("Issue %s is assigned to %s", ocpbugsId, assignee.Name)
	}

	impactStatementRequest := jira.Issue{
		Fields: &jira.IssueFields{
			Type:        jira.IssueType{Name: finalTaskType},
			Project:     jira.Project{Key: finalProject},
			Priority:    &jira.Priority{Name: "Critical"},
			Labels:      []string{updateblockers.LabelBlocker},
			Description: fmt.Sprintf(descriptionTemplate, ocpbugsId, ocpbugsId),
			Summary:     fmt.Sprintf("Impact statement request for %s %s", ocpbugsId, blockerCandidate.Fields.Summary),
		},
	}
	if assignee != nil {
		impactStatementRequest.Fields.Assignee = assignee
	}

	logrus.Infof("Creating impact statement request %s card in %s project", finalTaskType, finalProject)
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

	// Save mappings after successful card creation
	saveComponentMappingIfNeeded(componentName, o.componentProject, finalProject, m)
	saveTaskTypeMappingIfNeeded(finalProject, finalTaskType, m)
	
	if err := m.SaveMappings(); err != nil {
		logrus.WithError(err).Warn("Failed to save mappings, but card was created successfully")
	}

}

var descriptionTemplate = `We're asking the following questions to evaluate whether or not %s warrants changing update recommendations from either the previous X.Y or X.Y.Z. The ultimate goal is to avoid recommending an update which introduces new risk or reduces cluster functionality in any way. In the absence of a declared update risk (the status quo), there is some risk that the existing fleet updates into the at-risk releases. Depending on the bug and estimated risk, leaving the update risk undeclared may be acceptable.

Sample answers are provided to give more context and the {{ImpactStatementRequested}} label has been added to %s. When responding, please move this ticket to {{{}Code Review{}}}. The expectation is that the assignee answers these questions.

h2. Which 4.y.z to 4.y'.z' updates increase vulnerability?
 * reasoning: This allows us to populate [{{from}} and {{to}} in conditional update recommendations|https://github.com/openshift/cincinnati-graph-data/tree/0335e56cde6b17230106f137382cbbd9aa5038ed#block-edges] for "the {{$SOURCE_RELEASE}} to {{$TARGET_RELEASE}} update is exposed.
 * example: Customers upgrading from any 4.y (or specific 4.y.z) to 4.(y+1).z'. Use {{oc adm upgrade}} to show your current cluster version.

h2. Which types of clusters?
 * reasoning: This allows us to populate [{{matchingRules}} in conditional update recommendations|https://github.com/openshift/cincinnati-graph-data/tree/0335e56cde6b17230106f137382cbbd9aa5038ed#block-edges] for "clusters like {{{}$THIS{}}}".
 * example: GCP clusters with thousands of namespaces, approximately 5%% of the subscribed fleet. Check your vulnerability with {{oc ...}} or the following PromQL {{{}count (...) > 0{}}}. If PromQL is provided and the underlying bug might impact updates out of a [4.19|https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html-single/release_notes/index#ocp-4-19-monitoring-metrics-collection-profiles-ga] or newer cluster, please list [the metrics collection profiles|https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html-single/monitoring/index#choosing-a-metrics-collection-profile_configuring-performance-and-scalability] with which the PromQL works.

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
