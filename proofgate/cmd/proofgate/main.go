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
	"github.com/entireio/external-agents/proofgate/passportbuilder"
	"github.com/entireio/external-agents/proofgate/warehouse"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: proofgate <build-passport|gate|ci-gate|evaluate|export|default-policy> [flags]")
	}
	var err error
	switch os.Args[1] {
	case "build-passport":
		err = buildPassport(os.Args[2:], os.Stdout)
	case "gate":
		err = gate(os.Args[2:], os.Stdout)
	case "ci-gate":
		err = ciGate(os.Args[2:], os.Stdout)
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

type gateOutput struct {
	Passport contracts.ChangePassport `json:"passport"`
	Result   engine.Result            `json:"result"`
}

type buildOptions struct {
	repositoryPath     string
	repositoryID       string
	commit             string
	checkpointID       string
	intent             string
	testsPath          string
	repositoryOptedIn  bool
	repositoryIsPublic bool
	aiAuthored         bool
	sensitivePrefixes  string
	deniedPrefixes     string
	nowValue           string
}

func buildPassport(args []string, stdout io.Writer) error {
	options, err := parseBuildOptions("build-passport", args, false)
	if err != nil {
		return err
	}
	passport, _, err := buildFromOptions(options)
	if err != nil {
		return err
	}
	return writeJSON(stdout, passport)
}

func gate(args []string, stdout io.Writer) error {
	options, err := parseBuildOptions("gate", args, true)
	if err != nil {
		return err
	}
	passport, now, err := buildFromOptions(options)
	if err != nil {
		return err
	}
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		return err
	}
	return writeJSON(stdout, gateOutput{Passport: passport, Result: result})
}

func parseBuildOptions(name string, args []string, includeNow bool) (buildOptions, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options buildOptions
	flags.StringVar(&options.repositoryPath, "repo", ".", "path to the git repository")
	flags.StringVar(&options.repositoryID, "repo-id", "", "synthetic or approved repository identifier")
	flags.StringVar(&options.commit, "commit", "HEAD", "commit or revision to inspect")
	flags.StringVar(&options.checkpointID, "checkpoint", "auto", "Entire checkpoint ID, or auto for commit trailer")
	flags.StringVar(&options.intent, "intent", "", "safe intent summary; defaults to commit subject")
	flags.StringVar(&options.testsPath, "tests", "", "ProofGate test report JSON")
	flags.BoolVar(&options.repositoryOptedIn, "repo-opted-in", false, "confirm repository opt-in for governed export")
	flags.BoolVar(&options.repositoryIsPublic, "repo-public", false, "mark repository as public")
	flags.BoolVar(&options.aiAuthored, "ai-authored", true, "mark the change as AI-authored")
	flags.StringVar(&options.sensitivePrefixes, "sensitive-prefixes", "", "comma-separated sensitive path prefixes")
	flags.StringVar(&options.deniedPrefixes, "denied-prefixes", "", "comma-separated never-auto-approve path prefixes")
	if includeNow {
		flags.StringVar(&options.nowValue, "now", "", "evaluation time in RFC3339")
	}
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	return options, nil
}

func buildFromOptions(options buildOptions) (contracts.ChangePassport, time.Time, error) {
	var tests passportbuilder.TestReport
	if strings.TrimSpace(options.testsPath) != "" {
		if err := decodePath(options.testsPath, nil, &tests); err != nil {
			return contracts.ChangePassport{}, time.Time{}, fmt.Errorf("read tests: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	passport, err := (passportbuilder.Builder{}).Build(ctx, passportbuilder.Request{
		RepositoryPath:     options.repositoryPath,
		RepositoryID:       options.repositoryID,
		Commit:             options.commit,
		CheckpointID:       options.checkpointID,
		Intent:             options.intent,
		RepositoryOptedIn:  options.repositoryOptedIn,
		RepositoryIsPublic: options.repositoryIsPublic,
		AIAuthored:         options.aiAuthored,
		Tests:              tests,
		SensitivePrefixes:  splitCSVOrNil(options.sensitivePrefixes),
		DeniedPrefixes:     splitCSVOrNil(options.deniedPrefixes),
	})
	if err != nil {
		return contracts.ChangePassport{}, time.Time{}, err
	}
	now := time.Now().UTC()
	if strings.TrimSpace(options.nowValue) != "" {
		now, err = time.Parse(time.RFC3339, options.nowValue)
		if err != nil {
			return contracts.ChangePassport{}, time.Time{}, fmt.Errorf("parse --now: %w", err)
		}
	}
	return passport, now, nil
}

func splitCSVOrNil(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
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
	client, err := warehouseClientFromEnvironment()
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

func warehouseClientFromEnvironment() (*warehouse.Client, error) {
	return warehouse.NewClient(warehouse.Config{
		Host:        os.Getenv("DATABRICKS_HOST"),
		Token:       os.Getenv("DATABRICKS_TOKEN"),
		WarehouseID: os.Getenv("DATABRICKS_SQL_WAREHOUSE_ID"),
		Catalog:     os.Getenv("PROOFGATE_DATABRICKS_CATALOG"),
		Schema:      os.Getenv("PROOFGATE_DATABRICKS_SCHEMA"),
	})
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
