package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: proofgate <evaluate|default-policy> [flags]")
	}
	var err error
	switch os.Args[1] {
	case "evaluate":
		err = evaluate(os.Args[2:], os.Stdout)
	case "default-policy":
		err = writeJSON(os.Stdout, engine.DefaultPolicy())
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func evaluate(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	passportPath := flags.String("passport", "-", "passport JSON path, or - for stdin")
	policyPath := flags.String("policy", "", "optional policy JSON path")
	nowValue := flags.String("now", "", "evaluation time in RFC3339 for reproducible tests")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var passport contracts.ChangePassport
	if err := decodePath(*passportPath, os.Stdin, &passport); err != nil {
		return fmt.Errorf("read passport: %w", err)
	}
	policy := engine.DefaultPolicy()
	if strings.TrimSpace(*policyPath) != "" {
		if err := decodePath(*policyPath, nil, &policy); err != nil {
			return fmt.Errorf("read policy: %w", err)
		}
	}
	now := time.Now().UTC()
	if strings.TrimSpace(*nowValue) != "" {
		parsed, err := time.Parse(time.RFC3339, *nowValue)
		if err != nil {
			return fmt.Errorf("parse --now: %w", err)
		}
		now = parsed
	}
	result, err := engine.Evaluate(passport, policy, now)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func decodePath(path string, stdin io.Reader, value any) error {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		if stdin == nil {
			return errors.New("stdin is unavailable")
		}
		reader = stdin
	} else {
		opened, err := os.Open(path)
		if err != nil {
			return err
		}
		file = opened
		defer func() { _ = file.Close() }()
		reader = file
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
