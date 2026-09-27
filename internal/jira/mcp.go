package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orchestrator/internal/agent"
)

// MCPClient працює з Jira через Claude Code, у якого підключений Jira MCP
// (mcp-atlassian, конфіг — mcp/jira.json). Оркестратор просто просить Claude:
// "дістань задачу і поверни JSON" або "додай коментар".
//
// Налаштування (прапорці, модель) беруться з ролі "jira" у config.yaml.
type MCPClient struct {
	Runner            *agent.Runner
	WorkDir           string   // робоча папка для запуску claude
	Flags             []string // прапорці claude, серед них --mcp-config з Jira MCP
	Model             string   // необов'язково: модель (для цих простих дій вистачить haiku)
	SystemInstruction string
}

// run запускає claude з налаштуваннями ролі jira.
func (c *MCPClient) run(ctx context.Context, prompt string) (*agent.Result, error) {
	return c.Runner.Run(ctx, agent.Request{
		Role:              "jira",
		WorkDir:           c.WorkDir,
		Prompt:            prompt,
		SystemInstruction: c.SystemInstruction,
		Model:             c.Model,
		ExtraFlags:        c.Flags,
	})
}

func (c *MCPClient) GetIssue(ctx context.Context, key string) (*Issue, error) {
	prompt := fmt.Sprintf(`Використай Jira MCP і отримай задачу %s.
Відповідай ЛИШЕ одним JSON-об'єктом без пояснень і без markdown, у форматі:
{"key": "...", "summary": "...", "description": "...", "labels": ["..."]}
Якщо задачу отримати не вдалося — відповідай {"error": "<причина>"}.`, key)

	res, err := c.run(ctx, prompt)
	if err != nil {
		return nil, err
	}

	// Вбудована (embedded) структура: поля Issue + додаткове поле Error.
	var out struct {
		Issue
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(extractJSON(res.Text)), &out); err != nil {
		return nil, fmt.Errorf("Claude повернув не JSON: %w\nвідповідь: %s", err, res.Text)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("Jira MCP не зміг отримати задачу %s: %s", key, out.Error)
	}
	if out.Summary == "" {
		return nil, fmt.Errorf("Jira MCP повернув задачу %s без заголовка — схоже, MCP не спрацював\nвідповідь: %s", key, res.Text)
	}
	issue := out.Issue
	if issue.Key == "" {
		issue.Key = key
	}
	return &issue, nil
}

func (c *MCPClient) AddComment(ctx context.Context, key, text string) error {
	prompt := fmt.Sprintf("Використай Jira MCP і додай до задачі %s такий коментар (дослівно):\n\n%s", key, text)
	_, err := c.run(ctx, prompt)
	return err
}

// extractJSON вирізає текст від першої "{" до останньої "}" —
// на випадок, якщо модель все ж обгорнула JSON у ```json ... ```.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end < start {
		return s
	}
	return s[start : end+1]
}
