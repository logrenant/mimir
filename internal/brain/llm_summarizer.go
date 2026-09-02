package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// LLMSummarizer wraps the claude CLI to produce Turkish assessments.
type LLMSummarizer struct {
	cliPath string
	model   string
	timeout time.Duration
}

// NewLLMSummarizer creates a new summarizer based on config.
func NewLLMSummarizer(cfg config.Config) *LLMSummarizer {
	cliPath := cfg.ClaudeCLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	timeout := cfg.RefineTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &LLMSummarizer{
		cliPath: cliPath,
		model:   cfg.ClaudeModel,
		timeout: timeout,
	}
}

type cliResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
}

// SummarizeToTurkish takes raw content and generates a 1-paragraph Turkish summary,
// plus a few comma-separated tags.
// Returns (summary, tags, error).
func (l *LLMSummarizer) SummarizeToTurkish(ctx context.Context, content string) (string, []string, error) {
	systemPrompt := `Sen uzman bir sistem özetleyicisisin.
Görevin, sana verilen metni (chat kaydı, kod reposu veya not) okumak ve sadece 1 paragraflık, açık ve net bir Türkçe özet (Assessment) çıkarmaktır.
Ayrıca, metni en iyi tanımlayan 3-5 İngilizce etiketi (tags) belirlemelisin.

Formatın KESİNLİKLE şu olmalıdır (başka hiçbir şey yazma):
SUMMARY: [1 paragraflık Türkçe özet]
TAGS: [tag1, tag2, tag3]
`

	result, err := l.run(ctx, systemPrompt, content)
	if err != nil {
		return "", nil, err
	}

	return parseSummaryAndTags(result)
}

func parseSummaryAndTags(raw string) (string, []string, error) {
	lines := strings.Split(raw, "\n")
	var summary string
	var tags []string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SUMMARY:"):
			summary = strings.TrimSpace(strings.TrimPrefix(line, "SUMMARY:"))
		case strings.HasPrefix(line, "TAGS:"):
			tagStr := strings.TrimSpace(strings.TrimPrefix(line, "TAGS:"))
			tagStr = strings.Trim(tagStr, "[]")
			rawTags := strings.Split(tagStr, ",")
			for _, t := range rawTags {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		case summary != "" && len(tags) == 0 && line != "":
			// multi-line summary handling
			summary += " " + line
		}
	}
	
	if summary == "" {
		summary = raw // fallback
	}
	
	return summary, tags, nil
}

func (l *LLMSummarizer) run(ctx context.Context, systemPrompt, userContent string) (string, error) {
	disallowedTools := []string{
		"Bash", "Read", "Write", "Edit", "Grep", "Glob",
		"WebFetch", "WebSearch", "Task", "NotebookEdit", "TodoWrite",
	}

	args := []string{
		"-p",
		"--model", l.model,
		"--output-format", "json",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--restricted",
		"--effort", "low",
		"--system-prompt", systemPrompt,
		"--disallowedTools", strings.Join(disallowedTools, " "),
	}

	var stdout, stderr bytes.Buffer
	var lastErr error

	for attempt := 1; attempt <= 2; attempt++ {
		stdout.Reset()
		stderr.Reset()

		runCtx, cancel := context.WithTimeout(ctx, l.timeout)
		cmd := exec.CommandContext(runCtx, l.cliPath, args...)
		cmd.Stdin = strings.NewReader(userContent)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		lastErr = cmd.Run()
		cancel()

		if lastErr == nil {
			break
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ctx.Err()
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("claude CLI error: %w, stderr: %s", lastErr, stderr.String())
	}

	var raw cliResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return "", fmt.Errorf("failed to parse claude CLI JSON: %w", err)
	}

	if raw.IsError {
		return "", fmt.Errorf("claude CLI reported an error (subtype %s)", raw.Subtype)
	}

	return raw.Result, nil
}
