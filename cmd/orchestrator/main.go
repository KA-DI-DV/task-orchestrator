// Точка входу програми. У Go виконання завжди починається з функції
// main() у пакеті main.
//
// Тут лише описані CLI-команди (через бібліотеку cobra — її ж
// використовують kubectl, docker, gh). Уся логіка — у пакетах internal/.
//
// Команди:
//
//	orchestrator run <jira-url|KEY> [--repo main-repo] — виконати задачу (чекає до кінця)
//	orchestrator resume <KEY>                          — продовжити перервану/впавшу задачу
//	orchestrator status [KEY]                          — стан однієї або всіх задач
//	orchestrator logs <KEY>                            — логи агентів
//	orchestrator serve                                 — запустити REST API
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func main() {
	// Контекст, який скасується при Ctrl+C або `docker stop` (SIGTERM).
	// Його отримають усі частини програми, і вони коректно зупиняться.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Помилка:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "orchestrator",
		Short:         "Оркестратор AI-агентів: Jira → Developer → Reviewer → QA",
		SilenceUsage:  true, // не друкувати довідку при кожній помилці
		SilenceErrors: true, // помилку друкуємо самі в main()
	}
	// PersistentFlags — прапорці, доступні в усіх підкомандах.
	root.PersistentFlags().StringVar(&configPath, "config", envOr("CONFIG_PATH", "config.yaml"), "шлях до config.yaml")

	root.AddCommand(
		runCmd(&configPath),
		resumeCmd(&configPath),
		statusCmd(&configPath),
		logsCmd(&configPath),
		serveCmd(&configPath),
	)
	return root
}

func runCmd(configPath *string) *cobra.Command {
	var repo string
	cmd := &cobra.Command{
		Use:     "run <jira-url | KEY>",
		Short:   "Виконати задачу Jira від початку до кінця",
		Example: "  orchestrator run https://acme.atlassian.net/browse/PROJ-123\n  orchestrator run PROJ-123 --repo billing-api",
		Args:    cobra.ExactArgs(1), // рівно один аргумент
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := newApp(ctx, *configPath)
			if err != nil {
				return err
			}
			defer app.Close()

			task, err := app.Pipeline.Start(ctx, args[0], repo)
			if err != nil {
				return err
			}
			return app.Pipeline.Run(ctx, task.ID)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "основний репозиторій, у якому запускається claude (за замовчуванням orchestrator.main_repo або мітка repo:<name> у Jira)")
	return cmd
}

func resumeCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <KEY>",
		Short: "Продовжити задачу з місця зупинки (або перезапустити впавшу)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := newApp(ctx, *configPath)
			if err != nil {
				return err
			}
			defer app.Close()

			task, err := app.Pipeline.Resume(ctx, args[0])
			if err != nil {
				return err
			}
			return app.Pipeline.Run(ctx, task.ID)
		},
	}
}

func statusCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status [KEY]",
		Short: "Показати стан задач",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := newApp(ctx, *configPath)
			if err != nil {
				return err
			}
			defer app.Close()

			// Одна задача — детально.
			if len(args) == 1 {
				t, err := app.Store.GetTask(ctx, args[0])
				if err != nil {
					return err
				}
				fmt.Printf("Задача:       %s — %s\nСтатус:       %s\nГілка:        %s\nРев'ю:        %d/%d\nРепозиторії:\n",
					t.ID, t.Title, t.Status, t.BranchName, t.ReviewAttempts, t.MaxReviewAttempts)
				for _, r := range t.Repos {
					mark := ""
					if r.IsMain {
						mark = " (основний)"
					}
					fmt.Printf("  - %s%s  %s\n", r.RepoName, mark, r.WorktreePath)
				}
				if t.LastFeedback != "" {
					fmt.Printf("\nОстанній фідбек:\n%s\n", t.LastFeedback)
				}
				if t.LastError != "" {
					fmt.Printf("\nПомилка:\n%s\n", t.LastError)
				}
				return nil
			}

			// Усі задачі — таблицею. tabwriter вирівнює колонки.
			tasks, err := app.Store.ListTasks(ctx, 50)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tSTATUS\tMAIN REPO\tREVIEW\tUPDATED")
			for _, t := range tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%s\n",
					t.ID, t.Status, t.RepoName, t.ReviewAttempts, t.MaxReviewAttempts,
					t.UpdatedAt.Local().Format("2006-01-02 15:04")) // так у Go задається формат дати
			}
			return w.Flush()
		},
	}
}

func logsCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "logs <KEY>",
		Short: "Показати логи агентів для задачі",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := newApp(ctx, *configPath)
			if err != nil {
				return err
			}
			defer app.Close()

			logs, err := app.Store.ListLogs(ctx, args[0])
			if err != nil {
				return err
			}
			for _, l := range logs {
				fmt.Printf("\n━━━━━━ #%d %s (%s) %s ━━━━━━\n%s\n",
					l.ID, l.StepName, l.AgentRole, l.CreatedAt.Local().Format("15:04:05"), l.OutputLog)
			}
			return nil
		},
	}
}

func serveCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Запустити REST API сервер",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := newApp(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer app.Close()
			return app.Serve(cmd.Context())
		},
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
