package testreport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadJUnitAggregatesSuitesAndErrors(t *testing.T) {
	path := writeFixture(t, `<testsuites>
  <testsuite name="unit" tests="8" failures="1" errors="0" skipped="1" />
  <testsuite name="integration" tests="3" failures="0" errors="1" skipped="0" />
</testsuites>`)
	suite, err := ReadJUnit(path, "mobile", "gha-42", true)
	if err != nil {
		t.Fatal(err)
	}
	if suite.Status != "failed" || suite.Total != 11 || suite.Failed != 2 || !suite.Required {
		t.Fatalf("unexpected suite: %+v", suite)
	}
	if suite.EvidenceID != "gha-42" {
		t.Fatalf("unexpected evidence id: %s", suite.EvidenceID)
	}
}

func TestReadJUnitMarksFullySkippedRequiredSuite(t *testing.T) {
	path := writeFixture(t, `<testsuite name="e2e" tests="4" failures="0" errors="0" skipped="4"></testsuite>`)
	suite, err := ReadJUnit(path, "e2e", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if suite.Status != "skipped" || suite.Total != 4 || suite.Failed != 0 {
		t.Fatalf("unexpected suite: %+v", suite)
	}
}

func TestReadJUnitRejectsUnknownRootAndInvalidCounts(t *testing.T) {
	for _, fixture := range []string{
		`<coverage tests="1" />`,
		`<testsuite tests="1" failures="2" />`,
	} {
		if _, err := ReadJUnit(writeFixture(t, fixture), "bad", "", true); err == nil {
			t.Fatalf("expected fixture to fail: %s", fixture)
		}
	}
}

func writeFixture(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "junit.xml")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
