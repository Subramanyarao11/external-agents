package passportbuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const graphAnalysisSeconds = "20"

type impactAnalysis struct {
	Entities          []string
	MaxDependentCount int
	Source            string
	Complete          bool
	Warnings          []string
}

type graphReport struct {
	Files    []graphFile    `json:"files"`
	Warnings []graphWarning `json:"warnings"`
}

type graphFile struct {
	Path    string        `json:"path"`
	Changes []graphChange `json:"changes"`
}

type graphChange struct {
	Type            string `json:"type"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	DependentsCount int    `json:"dependents_count"`
}

type graphWarning struct {
	Code string `json:"code"`
}

func (builder Builder) buildImpact(ctx context.Context, directory, commit string, changedFiles []string, mode string) (impactAnalysis, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "off"
	}
	fallback := impactAnalysis{
		Entities: impactedEntities(changedFiles), Source: "git-path-heuristic",
		Complete: false, Warnings: []string{},
	}
	if mode == "off" {
		return fallback, nil
	}
	if mode != "auto" && mode != "required" {
		return impactAnalysis{}, fmt.Errorf("invalid graph mode %q; use off, auto, or required", mode)
	}

	output, err := builder.Runner.Run(
		ctx, directory, "entire", "graph", "commit", commit,
		"--json", "--max-seconds", graphAnalysisSeconds, "--repo", ".",
	)
	if err != nil {
		if mode == "required" {
			return impactAnalysis{}, fmt.Errorf("Entire Graph analysis required: %w", err)
		}
		fallback.Warnings = []string{"ENTIRE_GRAPH_UNAVAILABLE"}
		return fallback, nil
	}
	analysis, err := decodeGraphImpact(output, changedFiles)
	if err != nil {
		if mode == "required" {
			return impactAnalysis{}, fmt.Errorf("decode required Entire Graph analysis: %w", err)
		}
		fallback.Warnings = []string{"ENTIRE_GRAPH_INVALID_OUTPUT"}
		return fallback, nil
	}
	return analysis, nil
}

func decodeGraphImpact(data []byte, changedFiles []string) (impactAnalysis, error) {
	var report graphReport
	if err := json.Unmarshal(data, &report); err != nil {
		return impactAnalysis{}, err
	}
	allowed := make(map[string]struct{}, len(changedFiles))
	for _, path := range changedFiles {
		if path = safeGraphPath(path); path != "" {
			allowed[path] = struct{}{}
		}
	}
	entities := []string{}
	covered := map[string]struct{}{}
	maxDependents := 0
	for _, file := range report.Files {
		path := safeGraphPath(file.Path)
		if _, ok := allowed[path]; !ok {
			continue
		}
		for _, change := range file.Changes {
			name := strings.TrimSpace(change.Name)
			kind := safeGraphCategory(change.Kind, "symbol")
			changeType := safeGraphCategory(change.Type, "changed")
			if name == "" {
				continue
			}
			entities = append(entities, fmt.Sprintf("%s:%s:%s@%s", changeType, kind, name, path))
			covered[path] = struct{}{}
			maxDependents = max(maxDependents, max(0, change.DependentsCount))
		}
	}
	for path := range allowed {
		if _, ok := covered[path]; !ok {
			entities = append(entities, "file:"+path)
		}
	}
	slices.Sort(entities)
	entities = slices.Compact(entities)

	warnings := []string{}
	for _, warning := range report.Warnings {
		if code := safeGraphCategory(warning.Code, ""); code != "" {
			warnings = append(warnings, strings.ToUpper(code))
		}
	}
	slices.Sort(warnings)
	warnings = slices.Compact(warnings)
	if len(report.Files) == 0 && len(changedFiles) > 0 {
		warnings = append(warnings, "ENTIRE_GRAPH_EMPTY")
	}
	return impactAnalysis{
		Entities: entities, MaxDependentCount: maxDependents, Source: "entire-graph-v0.4.0",
		Complete: len(warnings) == 0 && len(covered) == len(allowed), Warnings: warnings,
	}, nil
}

func safeGraphPath(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if value == "." || value == "" || filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../") {
		return ""
	}
	return value
}

func safeGraphCategory(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			result.WriteRune(character)
		}
	}
	if result.Len() == 0 {
		return fallback
	}
	return result.String()
}
