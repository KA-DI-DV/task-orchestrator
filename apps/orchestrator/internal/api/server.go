// Package api — REST API оркестратора.
//
// Ендпоінти:
//
//	GET  /healthz                  — перевірка, що сервер живий
//	POST /api/tasks                — запустити задачу {"jira_url": "...", "repo": "..."}
//	GET  /api/tasks                — список задач
//	GET  /api/tasks/{id}           — стан задачі
//	GET  /api/tasks/{id}/logs      — історія запусків агентів (вердикти, фідбек, вартість)
//	GET  /api/tasks/{id}/activity  — живий журнал дій агентів (останні рядки)
//	POST /api/tasks/{id}/resume    — продовжити зупинену / перервану / впавшу задачу
//	POST /api/tasks/{id}/stop      — зупинити виконання (сесію агента можна відновити)
//	POST /api/tasks/{id}/retest    — повторно запустити QA {"instructions": "..."} (для готової / впалої задачі)
//	POST /api/tasks/{id}/plan/approve — схвалити план Архітектора і почати розробку
//	POST /api/tasks/{id}/plan/revise  — надіслати правки до плану {"comment": "..."}
//	DELETE /api/tasks/{id}         — видалити задачу з гілками (якщо ще не в main)
//	GET  /api/info                 — основний репозиторій і список репозиторіїв
//	GET  /api/limits               — останні відомі ліміти підписки Claude (використання, час скидання)
//	POST /api/limits/refresh       — оновити ліміти коротким запуском claude (haiku)
//	GET  /api/settings             — налаштування ролей (продовжувати сесію чи починати заново)
//	PUT  /api/settings             — зберегти налаштування ролей {"roles": [{"role": "...", "resume_session": true}]}
//
// Використовуємо лише стандартну бібліотеку net/http: починаючи з Go 1.22
// роутер вміє метод і параметри в шляху ("GET /api/tasks/{id}"),
// тож сторонні фреймворки (gin, echo, chi) тут не потрібні.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"orchestrator/internal/pipeline"
	"orchestrator/internal/storage"
)

type Server struct {
	pipeline *pipeline.Pipeline
	store    *storage.Store
	logDir   string // папка журналів дій агентів (<logDir>/<KEY>.log)

	// Контекст усього сервера. Задачі виконуються у фоні довше, ніж
	// живе HTTP-запит, тому їм не можна давати контекст запиту
	// (він скасовується одразу після відповіді).
	baseCtx context.Context
	// WaitGroup рахує фонові задачі, щоб при зупинці дочекатися їх завершення.
	wg sync.WaitGroup
}

func NewServer(ctx context.Context, p *pipeline.Pipeline, s *storage.Store, logDir string) *Server {
	return &Server{pipeline: p, store: s, logDir: logDir, baseCtx: ctx}
}

// Handler повертає роутер з усіма маршрутами.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("GET /api/tasks/{id}/logs", s.getLogs)
	mux.HandleFunc("GET /api/tasks/{id}/activity", s.getActivity)
	mux.HandleFunc("POST /api/tasks/{id}/resume", s.resumeTask)
	mux.HandleFunc("POST /api/tasks/{id}/stop", s.stopTask)
	mux.HandleFunc("POST /api/tasks/{id}/retest", s.retestTask)
	mux.HandleFunc("POST /api/tasks/{id}/plan/approve", s.approvePlan)
	mux.HandleFunc("POST /api/tasks/{id}/plan/revise", s.revisePlan)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	mux.HandleFunc("GET /api/info", s.getInfo)
	mux.HandleFunc("GET /api/limits", s.getLimits)
	mux.HandleFunc("POST /api/limits/refresh", s.refreshLimits)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.saveSettings)
	return mux
}

// Wait чекає, поки завершаться всі фонові задачі.
func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JiraURL string `json:"jira_url"`
		Repo    string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.JiraURL == "" {
		writeError(w, http.StatusBadRequest, errors.New(`очікую JSON {"jira_url": "...", "repo": "..."}`))
		return
	}

	task, err := s.pipeline.Start(r.Context(), req.JiraURL, req.Repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	s.runInBackground(task.ID)
	task.Running = true
	// 202 Accepted — "прийнято, виконується у фоні".
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) resumeTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.pipeline.Resume(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.runInBackground(task.ID)
	task.Running = true
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) stopTask(w http.ResponseWriter, r *http.Request) {
	if err := s.pipeline.Stop(r.PathValue("id")); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	// 202: зупинка асинхронна — агенту потрібно кілька секунд, щоб завершитись.
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (s *Server) retestTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Instructions string `json:"instructions"`
	}
	// Тіло необов'язкове: без нього — тестування без додаткових інструкцій.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.New(`очікую JSON {"instructions": "..."}`))
			return
		}
	}
	task, err := s.pipeline.Retest(r.Context(), r.PathValue("id"), req.Instructions)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.runInBackground(task.ID)
	task.Running = true
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request) {
	task, err := s.pipeline.ApprovePlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.runInBackground(task.ID)
	task.Running = true
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) revisePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New(`очікую JSON {"comment": "..."}`))
		return
	}
	task, err := s.pipeline.RevisePlan(r.Context(), r.PathValue("id"), req.Comment)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.runInBackground(task.ID)
	task.Running = true
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.pipeline.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, t := range tasks {
		t.Running = s.pipeline.IsRunning(t.ID)
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	// r.PathValue("id") — значення {id} з шаблону маршруту.
	task, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	task.Running = s.pipeline.IsRunning(task.ID)
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.store.ListLogs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// taskKeyRe — ключ задачі. Перевіряємо id, перш ніж підставити його в шлях до
// файлу: інакше запит на кшталт "../../etc/passwd" прочитав би чужий файл.
var taskKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-\d+$`)

// activityLines — скільки останніх рядків журналу віддавати.
const activityLines = 400

func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !taskKeyRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, errors.New("неправильний ключ задачі"))
		return
	}
	data, err := os.ReadFile(filepath.Join(s.logDir, id+".log"))
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string][]string{"lines": {}}) // агенти ще не запускались
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > activityLines {
		lines = lines[len(lines)-activityLines:]
	}
	writeJSON(w, http.StatusOK, map[string][]string{"lines": lines})
}

func (s *Server) getInfo(w http.ResponseWriter, r *http.Request) {
	repos, err := s.pipeline.Workspace.DiscoverRepos(s.pipeline.ExcludeRepos)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"main_repo":     s.pipeline.MainRepo,
		"repos":         repos,
		"exclude_repos": s.pipeline.ExcludeRepos,
	})
}

func (s *Server) getLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := s.store.ClaudeLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, limits) // null — claude ще жодного разу не повідомив ліміти
}

func (s *Server) refreshLimits(w http.ResponseWriter, r *http.Request) {
	// Помилку запуску не вважаємо фатальною: rate_limit_event приходить на
	// самому початку, тож ліміти могли оновитися навіть якщо claude потім упав
	// (наприклад, бо ліміт вичерпано).
	if err := s.pipeline.Runner.CheckLimits(r.Context()); err != nil {
		slog.Warn("перевірка лімітів Claude завершилась помилкою", "error", err)
	}
	s.getLimits(w, r)
}

type settingsBody struct {
	Roles  []storage.RoleSetting `json:"roles"`
	Models []string              `json:"models,omitempty"` // які моделі можна обрати (лише у відповіді)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	roles, err := s.store.RoleSettings(r.Context(), pipeline.AgentRoles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsBody{Roles: roles, Models: storage.Models})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New(`очікую JSON {"roles": [{"role": "...", "resume_session": true}]}`))
		return
	}
	for _, st := range req.Roles {
		if !slices.Contains(pipeline.AgentRoles, st.Role) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("невідома роль %q", st.Role))
			return
		}
		if !slices.Contains(storage.Models, st.Model) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("невідома модель %q", st.Model))
			return
		}
	}
	if err := s.store.SaveRoleSettings(r.Context(), req.Roles); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.getSettings(w, r)
}

// runInBackground запускає задачу в окремій goroutine ("легкому потоці").
// HTTP-відповідь повертається одразу, а задача працює далі.
func (s *Server) runInBackground(taskID string) {
	s.wg.Go(func() {
		if err := s.pipeline.Run(s.baseCtx, taskID); err != nil {
			slog.Error("задача завершилась з помилкою", "key", taskID, "error", err)
		}
	})
}

// ───────────── Допоміжні функції для JSON-відповідей ─────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func statusFor(err error) int {
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	// 409 Conflict — запит правильний, але стан задачі його не дозволяє.
	if errors.Is(err, pipeline.ErrMerged) || errors.Is(err, pipeline.ErrRunning) ||
		errors.Is(err, pipeline.ErrWrongState) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
