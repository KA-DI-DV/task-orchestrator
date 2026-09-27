// Package agent запускає Claude Code CLI як окремий процес.
//
// Фактично ми робимо те саме, що ти зробив би руками в терміналі:
//
//	cd /tmp/workspaces/worktree-PROJ-123
//	claude -p "Ось задача..." --output-format json --append-system-prompt "Ти developer..."
//
// Claude Code сам підтягує CLAUDE.md, .mcp.json, skills та інші налаштування
// з репозиторію, у якому його запустили. Тому оркестратору не треба знати
// правила кожного проєкту — достатньо запустити CLI у правильній папці.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// Request — що саме треба запустити.
type Request struct {
	Role              string   // developer / reviewer / tester (для логів)
	WorkDir           string   // папка, в якій запускати claude (worktree)
	Prompt            string   // основне завдання для агента
	SystemInstruction string   // додаткові інструкції ролі
	Model             string   // необов'язково: "opus", "sonnet", ...
	ExtraFlags        []string // прапорці з config.yaml (наприклад --dangerously-skip-permissions)
}

// Result — результат роботи агента.
type Result struct {
	Text     string        // фінальна відповідь Claude (поле "result" у JSON)
	RawLog   string        // повний вивід (stdout + stderr) — зберігаємо в БД
	CostUSD  float64       // вартість запуску
	NumTurns int           // скільки кроків зробив агент
	Duration time.Duration // скільки тривав запуск
}

// cliOutput описує JSON, який друкує `claude -p ... --output-format json`.
// Беремо лише потрібні нам поля — решту JSON-декодер просто пропустить.
type cliOutput struct {
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
}

// Runner вміє запускати claude. Timeout — максимальний час одного запуску.
type Runner struct {
	Binary  string // зазвичай просто "claude"
	Timeout time.Duration
}

// Run запускає агента і чекає, поки він закінчить.
func (r *Runner) Run(ctx context.Context, req Request) (*Result, error) {
	// Обмежуємо час роботи. Коли час вийде — процес claude буде вбито.
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel() // defer — "виконай це в кінці функції", щоб не забути звільнити ресурс

	args := []string{
		"-p", req.Prompt, // -p (print) — неінтерактивний режим: виконав і вийшов
		"--output-format", "json", // відповідь у вигляді JSON, який легко розібрати
	}
	if req.SystemInstruction != "" {
		args = append(args, "--append-system-prompt", req.SystemInstruction)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	// "..." розгортає слайс у окремі аргументи.
	args = append(args, req.ExtraFlags...)

	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = req.WorkDir

	// Збираємо вивід процесу в буфери пам'яті.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("запускаю агента", "role", req.Role, "dir", req.WorkDir)
	started := time.Now()
	runErr := cmd.Run()
	duration := time.Since(started)

	result := &Result{
		RawLog:   stdout.String() + "\n--- stderr ---\n" + stderr.String(),
		Duration: duration,
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("агент %s не вклався у %s", req.Role, r.Timeout)
	}

	// Пробуємо розібрати JSON, навіть якщо процес завершився з помилкою —
	// там часто є корисний опис проблеми.
	var out cliOutput
	if jsonErr := json.Unmarshal(stdout.Bytes(), &out); jsonErr != nil {
		if runErr != nil {
			return result, fmt.Errorf("claude завершився з помилкою: %w; stderr: %s", runErr, stderr.String())
		}
		return result, fmt.Errorf("не вдалося розібрати JSON від claude: %w", jsonErr)
	}

	result.Text = out.Result
	result.CostUSD = out.TotalCostUSD
	result.NumTurns = out.NumTurns

	if runErr != nil || out.IsError {
		return result, fmt.Errorf("агент %s повернув помилку: %s", req.Role, out.Result)
	}

	slog.Info("агент завершив роботу",
		"role", req.Role,
		"duration", duration.Round(time.Second),
		"turns", out.NumTurns,
		"cost_usd", out.TotalCostUSD,
	)
	return result, nil
}
