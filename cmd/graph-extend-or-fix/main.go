package main

import (
	"encoding/json"
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
)

type options struct {
	graphRepositoryPath string
	risk                string

	lastVersion string
	newVersion  string

	jira flagutil.JiraOptions
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.graphRepositoryPath, "graph-repository-path", "", "The path to the Cincinnati graph repository")
	fs.StringVar(&o.risk, "risk", "", "The identifier of the risk to extend or declare fixed")
	fs.StringVar(&o.lastVersion, "last", "", "Most recent version where the risk still exists")
	fs.StringVar(&o.newVersion, "new", "", "New version where the risk should either be extended or declared fixed")

	o.jira.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}

	return o
}

func (o *options) validate() error {
	if o.graphRepositoryPath == "" {
		return fmt.Errorf("--graph-repository-path must be specified and nonempty")
	}

	if o.risk == "" {
		return fmt.Errorf("--risk must be specified and empty")
	}

	if o.lastVersion == "" {
		return fmt.Errorf("--last must be specified and nonempty")
	}

	if o.newVersion == "" {
		return fmt.Errorf("--new must be specified and nonempty")
	}

	return o.jira.Validate(false)
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

func main() {
	// TODO(muller): Cobrify as ota graph ...
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	edgesDirectory := filepath.Join(o.graphRepositoryPath, "blocked-edges")
	lastVersionBlockPath := filepath.Join(edgesDirectory, fmt.Sprintf("%s-%s.yaml", o.lastVersion, o.risk))
	lastVersionBlockRaw, err := os.ReadFile(lastVersionBlockPath)
	if err != nil {
		logrus.WithError(err).Fatal("cannot read source file")
	}

	var lastVersionBlock ConditionallyBlockedEdge
	if err := yaml.Unmarshal(lastVersionBlockRaw, &lastVersionBlock); err != nil {
		logrus.WithError(err).Fatal("cannot unmarshal source file")
	}

	impactStatementCard := lastVersionBlock.URL
	if !strings.HasPrefix(impactStatementCard, "https://issues.redhat.com/browse/") {
		logrus.Warning("Blocked edge reference URL %s is not a Jira card", impactStatementCard)
		return
	}
	impactStatementCard = strings.TrimPrefix(impactStatementCard, "https://issues.redhat.com/browse/")

	jiraClient, err := o.jira.Client()
	if err != nil {
		logrus.WithError(err).Fatal("cannot create Jira client")
	}

	logrus.Infof("Obtaining (likely) impact statement card %s and process its linked bugs", impactStatementCard)
	blockerCandidate, err := jiraClient.GetIssue(impactStatementCard)
	if err != nil {
		logrus.WithError(err).Fatal("cannot get issue")
	}
	seen := sets.New[string]()
	bugs := map[string]*jira.Issue{}
	worklist := map[string]*jira.Issue{impactStatementCard: blockerCandidate}
	directBlocks := sets.New[string]()

	for len(worklist) > 0 {
		var key string
		var card *jira.Issue
		for k, v := range worklist {
			key = k
			card = v
			delete(worklist, key)
			break
		}

		if seen.Has(key) {
			logrus.Tracef("%s: Skipping already seen card", key)
			continue
		}
		seen.Insert(key)

		fmt.Printf("%s ", key)
		if strings.HasPrefix(key, "OCPBUGS-") {
			logrus.Tracef("%s: Found a bug card", key)
			bugs[key] = card
		}

		for _, link := range card.Fields.IssueLinks {
			if outward := link.OutwardIssue; outward != nil {
				if strings.HasPrefix(outward.Key, "OCPBUGS-") {
					linkedIssue, err := jiraClient.GetIssue(outward.Key)
					if err != nil {
						logrus.WithError(err).Fatal("cannot get issue")
					}
					worklist[outward.Key] = linkedIssue
					if key == blockerCandidate.Key && link.Type.Outward == "blocks" {
						directBlocks.Insert(outward.Key)
					}
				} else {
					logrus.Tracef("%s: not following a non-bug link '%s %s'", key, link.Type.Outward, outward.Key)
				}
			}
			if inward := link.InwardIssue; inward != nil {
				if strings.HasPrefix(inward.Key, "OCPBUGS-") {
					linkedIssue, err := jiraClient.GetIssue(inward.Key)
					if err != nil {
						logrus.WithError(err).Fatal("cannot get issue")
					}
					worklist[inward.Key] = linkedIssue
					if key == blockerCandidate.Key && link.Type.Inward == "blocks" {
						directBlocks.Insert(inward.Key)
					}
				} else {
					logrus.Tracef("%s: not following a non-bug link '%s %s'", key, link.Type.Inward, inward.Key)
				}
			}
		}
	}
	fmt.Printf("\n")

	logrus.Infof("Found %d bug cards", len(bugs))
	for key, bug := range bugs {
		targetVersion := ""
		if items, err := getIssueTargetVersion(bug); err == nil && len(items) > 0 {
			targetVersion = items[0].Name
			if len(items) > 1 {
				logrus.Warningf("%s: Found multiple target versions: %v", key, items)
			}
		}

		direct := ""
		if directBlocks.Has(key) {
			direct = "x"
		}
		// TODO(muller): Tabulate better, sort etc
		fmt.Printf("%s\t%-2s\t%s\t%-12s\t%s\n", key, direct, targetVersion, bug.Fields.Status.Name, bug.Fields.Summary)
	}

	// TODO(muller): Infer whether the bug is likely fixed or not
	// Likely only follow direct block links from the impact statement card and their clones
	// Unfixed (up to MOFIFIED?) bugs in higher or or equal versions are likely unfixed
	// No unfixed (up to MODIFIED) bugs in higher or equal versions are likely fixed
	// ON_QA and VERIFIED are hard to reason about: maybe check them in release controller diffs?
}

// Stolen from openshift-eng/jira-lifecycle-plugin
const (
	TargetVersionField    = "customfield_12319940"
	TargetVersionFieldOld = "customfield_12323140"
)

// getUnknownField will attempt to get the specified field from the Unknowns struct and unmarshal
// the value into the provided function. If the field is not set, the first return value of this
// function will return false.
func getUnknownField(field string, issue *jira.Issue, fn func() interface{}) (bool, error) {
	obj := fn()
	if issue.Fields == nil || issue.Fields.Unknowns == nil {
		return false, nil
	}
	unknownField, ok := issue.Fields.Unknowns[field]
	if !ok {
		return false, nil
	}
	bytes, err := json.Marshal(unknownField)
	if err != nil {
		return true, fmt.Errorf("failed to process the custom field %s. Error : %v", field, err)
	}
	if err := json.Unmarshal(bytes, obj); err != nil {
		return true, fmt.Errorf("failed to unmarshal the json to struct for %s. Error: %v", field, err)
	}
	return true, nil
}

func getIssueTargetVersion(issue *jira.Issue) ([]*jira.Version, error) {
	var obj *[]*jira.Version
	isSet, err := getUnknownField(TargetVersionField, issue, func() interface{} {
		obj = &[]*jira.Version{{}}
		return obj
	})
	if isSet && obj != nil && *obj != nil {
		return *obj, err
	}
	isSet, err = getUnknownField(TargetVersionFieldOld, issue, func() interface{} {
		obj = &[]*jira.Version{{}}
		return obj
	})
	if !isSet {
		return nil, err
	}
	return *obj, err
}
