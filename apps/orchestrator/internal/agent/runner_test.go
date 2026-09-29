package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Рядки у форматі `claude -p --output-format stream-json --verbose`.
func TestDescribe(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`{"type":"system","subtype":"init","session_id":"x"}`, nil},
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"Дивлюсь\nкод"}]}}`,
			[]string{"💬 Дивлюсь код"}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}`,
			[]string{"🔧 Bash: go test ./..."}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/main.go","old_string":"a","new_string":"b"}}]}}`,
			[]string{"🔧 Edit: /w/main.go"}},
		{`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"exit 1"}]}}`,
			[]string{`⚠️ помилка інструмента: "exit 1"`}},
		{`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`, nil},
	}
	for _, c := range cases {
		var ev event
		if err := json.Unmarshal([]byte(c.line), &ev); err != nil {
			t.Fatal(err)
		}
		if got := describe(&ev); !slices.Equal(got, c.want) {
			t.Errorf("describe(%s) = %q, очікував %q", c.line, got, c.want)
		}
	}
}

func TestResultEvent(t *testing.T) {
	var ev event
	line := `{"type":"result","subtype":"success","is_error":false,"result":"Готово\nVERDICT: APPROVE","total_cost_usd":0.42,"num_turns":7}`
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "result" || ev.Result != "Готово\nVERDICT: APPROVE" || ev.NumTurns != 7 || ev.TotalCostUSD != 0.42 {
		t.Errorf("неправильно розібрано: %+v", ev)
	}
}

// fakeClaude — скрипт замість claude: записує свої аргументи в args.txt,
// друкує подію result і, якщо в завданні є "wait", чекає, поки його зупинять.
func fakeClaude(t *testing.T) (binary, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	binary = filepath.Join(dir, "claude")
	script := `#!/bin/sh
echo "$@" > ` + argsFile + `
case "$2" in
  wait) trap 'exit 130' INT; while true; do sleep 0.1; done ;;
esac
echo '{"type":"result","result":"ok","num_turns":1}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, argsFile
}

func TestRunSessionFlags(t *testing.T) {
	bin, argsFile := fakeClaude(t)
	r := &Runner{Binary: bin, Timeout: 10 * time.Second}

	for _, tc := range []struct {
		resume bool
		want   string
	}{
		{false, "--session-id s-1"},
		{true, "--resume s-1"},
	} {
		if _, err := r.Run(context.Background(), Request{Prompt: "go", WorkDir: t.TempDir(), SessionID: "s-1", Resume: tc.resume}); err != nil {
			t.Fatal(err)
		}
		args, _ := os.ReadFile(argsFile)
		if !strings.Contains(string(args), tc.want) {
			t.Errorf("resume=%v: аргументи %q, очікував %q", tc.resume, args, tc.want)
		}
	}
}

// Зупинка: агент отримує Ctrl+C, а Run повертає причину зупинки.
func TestRunStop(t *testing.T) {
	bin, _ := fakeClaude(t)
	r := &Runner{Binary: bin, Timeout: 10 * time.Second}
	stopped := errors.New("зупинено")

	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(300*time.Millisecond, func() { cancel(stopped) })

	started := time.Now()
	_, err := r.Run(ctx, Request{Prompt: "wait", WorkDir: t.TempDir()})
	if !errors.Is(err, stopped) {
		t.Fatalf("Run = %v, очікував помилку з причиною %q", err, stopped)
	}
	if d := time.Since(started); d > 5*time.Second {
		t.Errorf("агент зупинявся %s — Ctrl+C не спрацював, чекали WaitDelay", d)
	}
}
