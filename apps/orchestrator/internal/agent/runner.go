// Package agent запускає Claude Code CLI як окремий процес.
//
// Фактично ми робимо те саме, що ти зробив би руками в терміналі:
//
//	cd /tmp/workspaces/PROJ-123/my-repo
//	claude -p "Ось задача..." --output-format stream-json --verbose --append-system-prompt "Ти developer..."
//
// Claude Code сам підтягує CLAUDE.md, .mcp.json, skills та інші налаштування
// з репозиторію, у якому його запустили. Тому оркестратору не треба знати
// правила кожного проєкту — достатньо запустити CLI у правильній папці.
//
// stream-json: claude друкує по одній JSON-події на рядок одразу, як вона
// стається (агент викликав інструмент, написав текст, отримав результат...).
// Останньою приходить подія "result" з фінальною відповіддю. Завдяки цьому
// видно, що агент робить прямо зараз, а не лише коли він закінчить.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Request — що саме треба запустити.
type Request struct {
	TaskID            string   // ключ задачі (PROJ-123) — для журналу дій
	Role              string   // developer / reviewer / tester (для логів)
	WorkDir           string   // папка, в якій запускати claude (worktree)
	Prompt            string   // основне завдання для агента
	SystemInstruction string   // додаткові інструкції ролі
	Model             string   // необов'язково: "opus", "sonnet", ...
	ExtraFlags        []string // прапорці з config.yaml (наприклад --dangerously-skip-permissions)
}

// Result — результат роботи агента.
type Result struct {
	Text     string        // фінальна відповідь Claude (поле "result" події result)
	RawLog   string        // журнал дій + фінальна подія + stderr — зберігаємо в БД
	CostUSD  float64       // вартість запуску
	NumTurns int           // скільки кроків зробив агент
	Duration time.Duration // скільки тривав запуск
}

// event — один рядок stream-json. Беремо лише потрібні поля —
// решту JSON-декодер просто пропустить.
type event struct {
	Type    string `json:"type"` // system / assistant / user / result
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`

	// Поля події "result".
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
}

// contentBlock — частина повідомлення: текст, виклик інструмента або його результат.
type contentBlock struct {
	Type    string          `json:"type"`     // text / tool_use / tool_result
	Text    string          `json:"text"`     // для text
	Name    string          `json:"name"`     // для tool_use: Bash, Read, Edit, mcp__...
	Input   json.RawMessage `json:"input"`    // для tool_use: аргументи інструмента
	IsError bool            `json:"is_error"` // для tool_result: інструмент завершився помилкою
	Content json.RawMessage `json:"content"`  // для tool_result: що повернув інструмент
}

// Runner вміє запускати claude. Timeout — максимальний час одного запуску.
type Runner struct {
	Binary  string // зазвичай просто "claude"
	Timeout time.Duration
	// LogDir — куди писати журнал дій агентів: <LogDir>/<TaskID>.log.
	// Порожній — журнал не пишемо (лише slog).
	LogDir string
}

// Run запускає агента і чекає, поки він закінчить.
func (r *Runner) Run(ctx context.Context, req Request) (*Result, error) {
	// Обмежуємо час роботи. Коли час вийде — процес claude буде вбито.
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel() // defer — "виконай це в кінці функції", щоб не забути звільнити ресурс

	args := []string{
		"-p", req.Prompt, // -p (print) — неінтерактивний режим: виконав і вийшов
		"--output-format", "stream-json", // по одній JSON-події на рядок, одразу
		"--verbose", // без нього claude -p не друкує stream-json
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

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// StdoutPipe — читаємо вивід поступово, поки процес ще працює.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	activity := r.openActivityLog(req.TaskID)
	defer activity.Close()

	slog.Info("запускаю агента", "role", req.Role, "dir", req.WorkDir)
	activity.write(req.Role, fmt.Sprintf("▶ старт (%s)", req.WorkDir))
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не вдалося запустити %s: %w", r.Binary, err)
	}

	// Читаємо події, поки claude не закриє stdout (тобто не завершиться).
	var final *event
	var journal strings.Builder // людиночитний журнал — піде в БД
	reader := bufio.NewReader(stdout)
	for {
		// ReadBytes, а не bufio.Scanner: у Scanner ліміт довжини рядка (64 КБ),
		// а результат інструмента (наприклад, cat великого файлу) буває більшим.
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var ev event
			if json.Unmarshal(line, &ev) == nil {
				if ev.Type == "result" {
					final = &ev
				}
				for _, msg := range describe(&ev) {
					slog.Info("агент", "role", req.Role, "do", msg)
					activity.write(req.Role, msg)
					journal.WriteString(msg + "\n")
				}
			}
		}
		if readErr != nil {
			break // io.EOF — процес закрив stdout
		}
	}

	runErr := cmd.Wait()
	duration := time.Since(started)

	result := &Result{Duration: duration}
	finalJSON, _ := json.Marshal(final)
	result.RawLog = journal.String() + "\n--- result ---\n" + string(finalJSON) + "\n--- stderr ---\n" + stderr.String()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		activity.write(req.Role, "⏱ перевищено таймаут")
		return result, fmt.Errorf("агент %s не вклався у %s", req.Role, r.Timeout)
	}
	if final == nil {
		if runErr != nil {
			return result, fmt.Errorf("claude завершився з помилкою: %w; stderr: %s", runErr, stderr.String())
		}
		return result, errors.New("claude не повернув фінальну подію result")
	}

	result.Text = final.Result
	result.CostUSD = final.TotalCostUSD
	result.NumTurns = final.NumTurns

	activity.write(req.Role, fmt.Sprintf("■ завершено за %s, кроків: %d, $%.2f",
		duration.Round(time.Second), final.NumTurns, final.TotalCostUSD))

	if runErr != nil || final.IsError {
		return result, fmt.Errorf("агент %s повернув помилку: %s", req.Role, final.Result)
	}

	slog.Info("агент завершив роботу",
		"role", req.Role,
		"duration", duration.Round(time.Second),
		"turns", final.NumTurns,
		"cost_usd", final.TotalCostUSD,
	)
	return result, nil
}

// describe перетворює подію на короткі рядки для людини:
// "🔧 Bash: go test ./...", "💬 Дивлюсь, як влаштовано ...", "⚠️ помилка інструмента: ...".
func describe(ev *event) []string {
	var out []string
	for _, c := range ev.Message.Content {
		switch {
		case ev.Type == "assistant" && c.Type == "text" && strings.TrimSpace(c.Text) != "":
			out = append(out, "💬 "+oneLine(c.Text, 300))
		case ev.Type == "assistant" && c.Type == "tool_use":
			out = append(out, "🔧 "+c.Name+": "+toolDetail(c.Input))
		case ev.Type == "user" && c.Type == "tool_result" && c.IsError:
			out = append(out, "⚠️ помилка інструмента: "+oneLine(string(c.Content), 200))
		}
	}
	return out
}

// toolDetail дістає з аргументів інструмента найцікавіше:
// команду для Bash, шлях для Read/Edit/Write, шаблон для Grep, URL для браузера.
func toolDetail(input json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "pattern", "url", "path", "description", "prompt"} {
		if v, ok := args[key].(string); ok && v != "" {
			return oneLine(v, 200)
		}
	}
	return oneLine(string(input), 200) // невідомий інструмент — показуємо аргументи як є
}

// oneLine стискає текст в один рядок і обрізає до max символів.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max { // []rune — щоб не розрізати кириличну літеру навпіл
		return string(r[:max]) + "…"
	}
	return s
}

// activityLog — файл журналу дій задачі. Усі етапи (developer, reviewer,
// tester) дописують у той самий файл, тож `tail -f` показує задачу від початку до кінця.
type activityLog struct {
	w io.WriteCloser
}

func (r *Runner) openActivityLog(taskID string) *activityLog {
	if r.LogDir == "" || taskID == "" {
		return &activityLog{}
	}
	if err := os.MkdirAll(r.LogDir, 0o755); err != nil {
		slog.Warn("не вдалося створити папку журналів", "dir", r.LogDir, "error", err)
		return &activityLog{}
	}
	f, err := os.OpenFile(filepath.Join(r.LogDir, taskID+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("не вдалося відкрити журнал дій", "task", taskID, "error", err)
		return &activityLog{}
	}
	return &activityLog{w: f}
}

func (a *activityLog) write(role, msg string) {
	if a.w != nil {
		fmt.Fprintf(a.w, "%s [%s] %s\n", time.Now().Format("15:04:05"), role, msg)
	}
}

func (a *activityLog) Close() {
	if a.w != nil {
		_ = a.w.Close()
	}
}
