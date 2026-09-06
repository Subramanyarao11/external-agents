package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/entireio/external-agents/proofgate/passportbuilder"
	"github.com/entireio/external-agents/proofgate/testreport"
)

type repeatedStrings []string

func (values *repeatedStrings) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStrings) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func junitReport(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("junit-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var requiredSpecs repeatedStrings
	var optionalSpecs repeatedStrings
	flags.Var(&requiredSpecs, "junit", "required suite as name=path; may be repeated")
	flags.Var(&optionalSpecs, "optional-junit", "optional suite as name=path; may be repeated")
	evidencePrefix := flags.String("evidence-prefix", defaultEvidencePrefix(), "opaque CI evidence ID prefix")
	outputPath := flags.String("output", "-", "output JSON path, or - for stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(requiredSpecs)+len(optionalSpecs) == 0 {
		return errors.New("at least one --junit or --optional-junit is required")
	}
	report := passportbuilder.TestReport{Suites: []passportbuilder.TestSuite{}}
	seen := map[string]struct{}{}
	for _, group := range []struct {
		specs    []string
		required bool
	}{{requiredSpecs, true}, {optionalSpecs, false}} {
		for _, spec := range group.specs {
			name, path, err := parseJUnitSpec(spec)
			if err != nil {
				return err
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("duplicate JUnit suite name %q", name)
			}
			seen[name] = struct{}{}
			evidenceID := strings.Trim(strings.TrimSpace(*evidencePrefix)+":"+name, ":")
			suite, err := testreport.ReadJUnit(path, name, evidenceID, group.required)
			if err != nil {
				return fmt.Errorf("read JUnit suite %q: %w", name, err)
			}
			report.Suites = append(report.Suites, suite)
		}
	}
	if *outputPath == "-" {
		return writeJSON(stdout, report)
	}
	return writeJSONFileAtomic(*outputPath, report)
}

func parseJUnitSpec(value string) (string, string, error) {
	name, path, found := strings.Cut(value, "=")
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	if !found || name == "" || path == "" {
		return "", "", fmt.Errorf("invalid JUnit spec %q; expected name=path", value)
	}
	if strings.ContainsAny(name, "\r\n") {
		return "", "", errors.New("JUnit suite name contains a newline")
	}
	return name, path, nil
}

func defaultEvidencePrefix() string {
	runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID"))
	if runID == "" {
		return "local-test-run"
	}
	attempt := strings.TrimSpace(os.Getenv("GITHUB_RUN_ATTEMPT"))
	if attempt == "" {
		attempt = "1"
	}
	return "github-run-" + runID + "-attempt-" + attempt
}
