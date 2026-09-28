package jira

import "testing"

// Тести в Go — це функції TestXxx(t *testing.T) у файлах *_test.go.
// Запуск: go test ./...
//
// Тут використано "табличний" стиль: список випадків і один цикл,
// який перевіряє кожен. Це найпоширеніший стиль тестів у Go.
func TestParseIssueKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"PROJ-123", "PROJ-123"},
		{"  PROJ-7 ", "PROJ-7"},
		{"https://acme.atlassian.net/browse/ABC-42", "ABC-42"},
		{"https://acme.atlassian.net/jira/software/projects/ABC/boards/1?selectedIssue=ABC-9", "ABC-9"},
	}

	for _, c := range cases {
		got, err := ParseIssueKey(c.input)
		if err != nil {
			t.Errorf("ParseIssueKey(%q) повернув помилку: %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseIssueKey(%q) = %q, очікував %q", c.input, got, c.want)
		}
	}
}

func TestParseIssueKeyInvalid(t *testing.T) {
	if _, err := ParseIssueKey("https://example.com/nothing-here"); err == nil {
		t.Error("очікував помилку для посилання без ключа")
	}
}

func TestRepoFromLabels(t *testing.T) {
	if got := RepoFromLabels([]string{"backend", "repo:billing-api"}); got != "billing-api" {
		t.Errorf("отримав %q", got)
	}
	if got := RepoFromLabels([]string{"backend"}); got != "" {
		t.Errorf("очікував порожній рядок, отримав %q", got)
	}
}
