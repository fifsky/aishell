package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestExtractExecCommand(t *testing.T) {
	content := "说明\n\n```shell-exec\npwd\nls -la\n```\n后续"

	got := extractExecCommand(content)
	want := "pwd\nls -la"
	if got != want {
		t.Fatalf("extractExecCommand() = %q, want %q", got, want)
	}
}

func TestStreamFilterHidesShellExec(t *testing.T) {
	filter := &streamFilter{}

	if _, err := filter.WriteString("hello\n```shell-exec\npwd\n```\nworld"); err != nil {
		t.Fatal(err)
	}
	if err := filter.Flush(); err != nil {
		t.Fatal(err)
	}

	got := filter.String()
	want := "hello\nworld\n"
	if got != want {
		t.Fatalf("filtered stream = %q, want %q", got, want)
	}
}

func TestAutoApprovePrefix(t *testing.T) {
	if !isAutoApprovedCommand("  tvly search \"OpenAI\" --json") {
		t.Fatal("expected tvly search command to be auto approved")
	}
	if isAutoApprovedCommand("tvly extract https://example.com") {
		t.Fatal("did not expect non-whitelisted tvly command to be auto approved")
	}
}

func TestParseSkillFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	data := `---
name: tavily-search
description: |
  Search the web.
  Returns JSON.
allowed-tools: Bash(tvly *)
---

# body
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, err := parseSkillFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name() != "tavily-search" {
		t.Fatalf("skill.Name() = %q", skill.Name())
	}
	if !strings.Contains(skill.Description(), "Search the web.") {
		t.Fatalf("skill.Description() = %q", skill.Description())
	}
	if skill.AllowedTools() != "Bash(tvly *)" {
		t.Fatalf("skill.AllowedTools() = %q", skill.AllowedTools())
	}
}

func TestBuildSystemPromptIncludesSkills(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "system_prompt.md")
	if err := os.WriteFile(promptPath, []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(dir, "skills", "demo")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillsDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: demo-skill\ndescription: Use demo tool.\nallowed-tools: Bash(demo *)\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, err := buildSystemPrompt(config{SystemPromptFile: promptPath, SkillsDir: filepath.Join(dir, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base prompt", "demo-skill", "Use demo tool.", "Bash(demo *)", skillsDir} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt should contain %q, got %q", want, prompt)
		}
	}
}

func TestUpdateContextKeepsSystemAndTail(t *testing.T) {
	home := t.TempDir()
	cfg := config{
		MaxContext: 3,
		BaseDir:    filepath.Join(home, ".aishell"),
		SessionDir: filepath.Join(home, ".aishell", "sessions"),
		ConfigFile: filepath.Join(home, ".aishell", "config.json"),
	}
	if err := initConfig(cfg); err != nil {
		t.Fatal(err)
	}
	contextFile := filepath.Join(cfg.SessionDir, defaultSession+".json")
	if err := writeMessages(contextFile, []message{{Role: "system", Content: "sys"}}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"one", "two", "three"} {
		if err := updateContext(cfg, "user", value); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := readMessages(contextFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	if messages[0].Role != "system" || messages[1].Content != "two" || messages[2].Content != "three" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestOpenAIStreamSendsDeltasAndKeepsFullContent(t *testing.T) {
	content := "说明\n```shell-exec\npwd\n```\n结论"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()

	cfg := config{
		APIURL: server.URL + "/chat/completions",
		APIKey: "test",
		Model:  "test-model",
	}
	var rendered streamFilter
	got, err := openAIStream(context.Background(), cfg, []message{{Role: "system", Content: "sys"}}, func(msg tea.Msg) {
		delta, ok := msg.(deltaMsg)
		if !ok {
			return
		}
		_, _ = rendered.WriteString(string(delta))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rendered.Flush(); err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Fatalf("full content = %q, want %q", got, content)
	}
	if strings.Contains(rendered.String(), "pwd") || !strings.Contains(rendered.String(), "说明") || !strings.Contains(rendered.String(), "结论") {
		t.Fatalf("rendered output should hide shell-exec block and keep visible markdown, got %q", rendered.String())
	}
}
