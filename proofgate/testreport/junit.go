package testreport

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/entireio/external-agents/proofgate/passportbuilder"
)

const maximumJUnitBytes = 10 << 20

type junitDocument struct {
	XMLName  xml.Name     `xml:""`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type counts struct {
	total   int
	failed  int
	skipped int
}

func ReadJUnit(path, suiteName, evidenceID string, required bool) (passportbuilder.TestSuite, error) {
	path = strings.TrimSpace(path)
	suiteName = strings.TrimSpace(suiteName)
	if path == "" || suiteName == "" {
		return passportbuilder.TestSuite{}, errors.New("JUnit path and suite name are required")
	}
	file, err := os.Open(path)
	if err != nil {
		return passportbuilder.TestSuite{}, err
	}
	defer func() { _ = file.Close() }()
	if stat, statErr := file.Stat(); statErr == nil && stat.Size() > maximumJUnitBytes {
		return passportbuilder.TestSuite{}, fmt.Errorf("JUnit report exceeds %d bytes", maximumJUnitBytes)
	}
	limited := io.LimitReader(file, maximumJUnitBytes+1)
	decoder := xml.NewDecoder(limited)
	decoder.Strict = true
	var document junitDocument
	if err := decoder.Decode(&document); err != nil {
		return passportbuilder.TestSuite{}, fmt.Errorf("decode JUnit XML: %w", err)
	}
	var result counts
	switch document.XMLName.Local {
	case "testsuite":
		result = counts{total: document.Tests, failed: document.Failures + document.Errors, skipped: document.Skipped}
		if result.total == 0 && len(document.Suites) > 0 {
			result = aggregateSuites(document.Suites)
		}
	case "testsuites":
		result = counts{total: document.Tests, failed: document.Failures + document.Errors, skipped: document.Skipped}
		children := aggregateSuites(document.Suites)
		if result.total == 0 {
			result.total = children.total
		}
		if result.failed == 0 {
			result.failed = children.failed
		}
		if result.skipped == 0 {
			result.skipped = children.skipped
		}
	default:
		return passportbuilder.TestSuite{}, fmt.Errorf("unsupported JUnit root %q", document.XMLName.Local)
	}
	if result.total < 0 || result.failed < 0 || result.skipped < 0 || result.failed > result.total || result.skipped > result.total {
		return passportbuilder.TestSuite{}, errors.New("JUnit report contains invalid counts")
	}
	status := "passed"
	if result.failed > 0 {
		status = "failed"
	} else if result.total == 0 || result.skipped == result.total {
		status = "skipped"
	}
	return passportbuilder.TestSuite{
		Name:       suiteName,
		Status:     status,
		Total:      result.total,
		Failed:     result.failed,
		Required:   required,
		EvidenceID: strings.TrimSpace(evidenceID),
	}, nil
}

func aggregateSuites(suites []junitSuite) counts {
	var result counts
	for _, suite := range suites {
		current := counts{total: suite.Tests, failed: suite.Failures + suite.Errors, skipped: suite.Skipped}
		if current.total == 0 && len(suite.Suites) > 0 {
			current = aggregateSuites(suite.Suites)
		}
		result.total += current.total
		result.failed += current.failed
		result.skipped += current.skipped
	}
	return result
}
