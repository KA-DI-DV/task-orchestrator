// Package jira — отримання даних задачі з Jira та запис коментарів.
//
// Оркестратор сам у Jira не ходить: з Jira працює Claude Code через
// Jira MCP (див. MCPClient і mcp/jira.json).
//
// Решта коду залежить лише від інтерфейсу Client — так у тестах його
// легко підмінити, а за потреби додати іншу реалізацію.
package jira

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Issue — те, що нам потрібно знати про задачу.
type Issue struct {
	Key         string   `json:"key"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Labels      []string `json:"labels"`
}

// Client — інтерфейс. У Go інтерфейс — це просто список методів.
// Будь-який тип, який має ці методи, автоматично "реалізує" інтерфейс
// (не треба писати implements, як у Java/TypeScript).
type Client interface {
	GetIssue(ctx context.Context, key string) (*Issue, error)
	AddComment(ctx context.Context, key, text string) error
}

// issueKeyRe шукає ключ задачі: великі літери/цифри, дефіс, число. Напр. PROJ-123.
var issueKeyRe = regexp.MustCompile(`[A-Z][A-Z0-9_]+-\d+`)

// ParseIssueKey дістає ключ задачі з того, що передав користувач.
//
// Підтримує:
//
//	PROJ-123
//	https://x.atlassian.net/browse/PROJ-123
//	https://x.atlassian.net/jira/software/projects/PROJ/boards/1?selectedIssue=PROJ-123
func ParseIssueKey(input string) (string, error) {
	input = strings.TrimSpace(input)

	// Якщо в посиланні є selectedIssue=..., беремо саме його.
	if _, after, found := strings.Cut(input, "selectedIssue="); found {
		if key := issueKeyRe.FindString(after); key != "" {
			return key, nil
		}
	}

	if key := issueKeyRe.FindString(input); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("не знайшов ключ задачі Jira у %q (очікую щось на кшталт PROJ-123)", input)
}

// RepoFromLabels шукає мітку виду "repo:my-service" і повертає "my-service".
// Так можна прямо в Jira вказати, у якому репозиторії робити задачу.
func RepoFromLabels(labels []string) string {
	for _, label := range labels {
		if name, found := strings.CutPrefix(label, "repo:"); found {
			return name
		}
	}
	return ""
}
