// Точка входу програми. У Go виконання завжди починається з функції
// main() у пакеті main.
//
// Оркестратор — це лише REST API сервер. Задачі створюються, продовжуються
// і переглядаються через адмін-панель (apps/admin), яка ходить у цей API.
// Уся логіка — у пакетах internal/, а збирає їх докупи app.go.
//
// Шлях до конфігу задає змінна оточення CONFIG_PATH (за замовчуванням config.yaml).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Контекст, який скасується при Ctrl+C або `docker stop` (SIGTERM).
	// Його отримають усі частини програми, і вони коректно зупиняться.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Помилка:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	app, err := newApp(ctx, envOr("CONFIG_PATH", "config.yaml"))
	if err != nil {
		return err
	}
	defer app.Close()
	return app.Serve(ctx)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
