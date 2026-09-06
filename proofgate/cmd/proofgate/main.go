package main

import (
	"context"
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
	"github.com/entireio/external-agents/proofgate/warehouse"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: proofgate <evaluate|export|default-policy> [flags]")
	}
	var err error
	switch os.Args[1] {
	case "evaluate":
		err = evaluate(os.Args[2:], os.Stdout)
	case "default-policy":
		err = writeJSON(os.Stdout, engine.DefaultPolicy())
	case "export":
		err = export(os.Args[2:], os.Stdout)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func export(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	passportPath := flags.String("passport", "-", "passport JSON path, or - for stdin")
	policyPath := flags.String("policy", "", "optional policy JSON path")
	nowValue := flags.String("now", "", "evaluation time in RFC3339")
	dryRun := flags.Bool("dry-run", false, "print the allowlisted event without sending it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	passport, policy, now, err := readEvaluationInputs(*passportPath, *policyPath, *nowValue, os.Stdin)
	if err != nil {
		return err
	}
	result, err := engine.Evaluate(passport, policy, now)
	if err != nil {
		return err
	}
	event, err := warehouse.BuildEvent(passport, result)
	if err != nil {
		return err
	}
	if *dryRun {
		return writeJSON(stdout, event)
	}
	client, err := warehouse.NewClient(warehouse.Config{
		Host:        os.Getenv("DATABRICKS_HOST"),
		Token:       os.Getenv("DATABRICKS_TOKEN"),
		WarehouseID: os.Getenv("DATABRICKS_SQL_WAREHOUSE_ID"),
		Catalog:     os.Getenv("PROOFGATE_DATABRICKS_CATALOG"),
		Schema:      os.Getenv("PROOFGATE_DATABRICKS_SCHEMA"),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ingested, err := client.Ingest(ctx, event)
	if err != nil {
		return err
	}
	return writeJSON(stdout, ingested)
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

	passport, policy, now, err := readEvaluationInputs(*passportPath, *policyPath, *nowValue, os.Stdin)
	if err != nil {
		return err
	}
	result, err := engine.Evaluate(passport, policy, now)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func readEvaluationInputs(passportPath, policyPath, nowValue string, stdin io.Reader) (contracts.ChangePassport, engine.Policy, time.Time, error) {
	var passport contracts.ChangePassport
	if err := decodePath(passportPath, stdin, &passport); err != nil {
		return passport, engine.Policy{}, time.Time{}, fmt.Errorf("read passport: %w", err)
	}
	policy := engine.DefaultPolicy()
	if strings.TrimSpace(policyPath) != "" {
		if err := decodePath(policyPath, nil, &policy); err != nil {
			return passport, engine.Policy{}, time.Time{}, fmt.Errorf("read policy: %w", err)
		}
	}
	now := time.Now().UTC()
	if strings.TrimSpace(nowValue) != "" {
		parsed, err := time.Parse(time.RFC3339, nowValue)
		if err != nil {
			return passport, engine.Policy{}, time.Time{}, fmt.Errorf("parse --now: %w", err)
		}
		now = parsed
	}
	return passport, policy, now, nil
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
