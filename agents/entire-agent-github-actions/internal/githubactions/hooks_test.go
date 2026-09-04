package githubactions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedActionLifecycle(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("ENTIRE_REPO_ROOT", repo)
	agent := New()

	count, err := agent.InstallHooks(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !agent.AreHooksInstalled() {
		t.Fatalf("first install = %d, installed = %v", count, agent.AreHooksInstalled())
	}
	count, err = agent.InstallHooks(false, false)
	if err != nil || count != 0 {
		t.Fatalf("idempotent install = %d, %v", count, err)
	}
	if err := agent.UninstallHooks(); err != nil {
		t.Fatal(err)
	}
	if agent.AreHooksInstalled() {
		t.Fatal("managed action remains installed")
	}
}

func TestManagedActionIsProviderNeutral(t *testing.T) {
	action := generatedAction(false)
	for _, expected := range []string{
		"Capture a Claude, Codex, or Cursor execution",
		"provider:",
		"--provider \"$ENTIRE_PROVIDER\"",
		"--model \"$ENTIRE_MODEL\"",
	} {
		if !strings.Contains(action, expected) {
			t.Errorf("generated action does not contain %q", expected)
		}
	}
}

func TestCheckedInActionMatchesInstaller(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", actionRelativePath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != generatedAction(false) {
		t.Fatal("checked-in capture action differs from the external-agent installer output")
	}
}

func TestInstallDoesNotOverwriteForeignAction(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("ENTIRE_REPO_ROOT", repo)
	path := filepath.Join(repo, actionRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := New().InstallHooks(false, false); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected foreign-action safety error, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "name: user-owned\n" {
		t.Fatalf("foreign action was changed: %s", data)
	}
}

func TestParseLifecycleHook(t *testing.T) {
	t.Parallel()
	input := []byte(`{"session_id":"run-42","session_ref":"/tmp/session.json","timestamp":"2026-09-04T12:00:00Z","user_prompt":"Fix checkout","raw_data":{"model":"claude-sonnet-4-6","repository":"org/repo"}}`)
	event, err := New().ParseHook(HookTurnStart, input)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != 2 || event.SessionID != "run-42" || event.Prompt != "Fix checkout" || event.Model != "claude-sonnet-4-6" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Metadata["repository"] != "org/repo" {
		t.Fatalf("unexpected metadata: %+v", event.Metadata)
	}
}

func TestSessionDirectoryIsRepoScopedOutsideRunner(t *testing.T) {
	t.Setenv("RUNNER_TEMP", "")
	agent := New()
	first, err := agent.GetSessionDir("/tmp/repo-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := agent.GetSessionDir("/tmp/repo-two")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("repo-scoped session directories collided: %s", first)
	}
	if !strings.Contains(first, string(filepath.Separator)+"entire-github-actions"+string(filepath.Separator)) {
		t.Fatalf("unexpected session directory: %s", first)
	}
}
