package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type options struct {
	graphRepositoryPath string

	risk        string
	fromVersion string
}

func gatherOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.graphRepositoryPath, "graph-repository-path", "", "The path to the Cincinnati graph repository")

	fs.StringVar(&o.risk, "risk", "", "The identifier of the risk to be updates")
	fs.StringVar(&o.fromVersion, "from", "", "The version where the risk was updated manually and its changes should propagate everywhere")

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

	if o.fromVersion == "" {
		return fmt.Errorf("--from must be specified and nonempty")
	}

	return nil
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
	// TODO(muller): Cobrify as ota graph spread-edge-changes
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	edgesDirectory := filepath.Join(o.graphRepositoryPath, "blocked-edges")
	sourcePath := filepath.Join(edgesDirectory, fmt.Sprintf("%s-%s.yaml", o.fromVersion, o.risk))
	sourceRaw, err := os.ReadFile(sourcePath)
	if err != nil {
		logrus.WithError(err).Fatal("cannot read source file")
	}

	var source ConditionallyBlockedEdge
	if err := yaml.Unmarshal(sourceRaw, &source); err != nil {
		logrus.WithError(err).Fatal("cannot unmarshal source file")
	}

	if err := filepath.WalkDir(edgesDirectory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logrus.WithError(err).Error("Failure when walking items in graph repository directory %s", edgesDirectory)
			return err
		}

		if d.IsDir() {
			logrus.Trace("Skipping (unexpected) directory %s", path)
			return nil
		}

		targetRaw, err := os.ReadFile(path)
		if err != nil {
			logrus.WithError(err).Error("Cannot read target file %s", path)
			return err
		}

		var target ConditionallyBlockedEdge
		if err := yaml.Unmarshal(targetRaw, &target); err != nil {
			logrus.WithError(err).Error("Cannot unmarshal target file %s", path)
			return err
		}

		if target.Name != o.risk {
			logrus.Trace("Skipping target file %s because it does not match the risk %s", path, o.risk)
			return nil
		}

		target.Message = source.Message
		target.URL = source.URL
		target.MatchingRules = source.MatchingRules
		// TODO(muller): Handle `from` field, will be likely identical within minor

		targetFile, err := os.Create(path)
		if err != nil {
			logrus.WithError(err).Error("Cannot open target file %s")
		}
		defer func(targetFile *os.File) {
			_ = targetFile.Close()
		}(targetFile)

		encoder := yaml.NewEncoder(targetFile)
		encoder.SetIndent(1)
		if err := encoder.Encode(target); err != nil {
			logrus.WithError(err).Error("Cannot marshal updated edge into target file %s", path)
		}
		return err
	}); err != nil {
		logrus.WithError(err).Fatal("cannot walk graph repository")
	}
}
