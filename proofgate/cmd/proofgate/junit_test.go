package main

import "testing"

func TestParseJUnitSpec(t *testing.T) {
	name, path, err := parseJUnitSpec("mobile = reports/junit.xml")
	if err != nil {
		t.Fatal(err)
	}
	if name != "mobile" || path != "reports/junit.xml" {
		t.Fatalf("unexpected spec %q %q", name, path)
	}
	if _, _, err := parseJUnitSpec("missing-path"); err == nil {
		t.Fatal("expected invalid spec to fail")
	}
}
