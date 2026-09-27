// Package storage — робота з базою даних PostgreSQL.
//
// Використовуємо бібліотеку pgx (v5) — найпопулярніший і найшвидший
// драйвер PostgreSQL для Go. Жодного ORM: пишемо звичайний SQL, щоб
// завжди було видно, що саме відбувається з базою.
package storage

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Директива go:embed "вшиває" SQL-файли прямо в скомпільований бінарник.
// Тобто в Docker-образ не треба окремо копіювати папку migrations.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store — наш доступ до бази. Всередині — пул з'єднань (pgxpool.Pool),
// який сам відкриває/перевикористовує з'єднання і безпечний для
// одночасного використання з багатьох goroutine.
type Store struct {
	pool *pgxpool.Pool
}

// Connect підключається до PostgreSQL і чекає, поки база стане доступною.
//
// context.Context — стандартний спосіб у Go передавати "сигнал скасування"
// і таймаути. Якщо користувач натисне Ctrl+C, ctx скасується, і всі
// операції, яким ми його передали, зупиняться.
func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("неправильний DATABASE_URL: %w", err)
	}

	// База в Docker може стартувати на кілька секунд пізніше — пробуємо кілька разів.
	for attempt := 1; ; attempt++ {
		err = pool.Ping(ctx)
		if err == nil {
			break
		}
		if attempt == 10 {
			pool.Close()
			return nil, fmt.Errorf("не вдалося підключитися до PostgreSQL: %w", err)
		}
		slog.Warn("PostgreSQL ще недоступний, повторюю...", "attempt", attempt, "error", err)
		time.Sleep(2 * time.Second)
	}

	return &Store{pool: pool}, nil
}

// Close закриває всі з'єднання з базою.
func (s *Store) Close() {
	s.pool.Close()
}

// Migrate створює таблиці, якщо їх ще немає.
//
// Як це працює:
//  1. Є службова таблиця schema_migrations, де записано, які файли вже застосовані.
//  2. Беремо всі файли migrations/*.sql, сортуємо за назвою (001_, 002_, ...).
//  3. Кожен ще не застосований файл виконуємо в транзакції.
//
// Щоб змінити схему в майбутньому — додай файл 002_щось.sql. Старі файли не чіпай.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("створення schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, file := range files {
		var alreadyApplied bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, file,
		).Scan(&alreadyApplied)
		if err != nil {
			return err
		}
		if alreadyApplied {
			continue
		}

		sqlText, err := migrationsFS.ReadFile(file)
		if err != nil {
			return err
		}

		// Транзакція: або виконується все, або нічого.
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		// Зазвичай тут пишуть `defer tx.Rollback(ctx)`, але defer спрацьовує
		// лише при виході з УСІЄЇ функції, а ми в циклі — тому Rollback вручну.
		if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("міграція %s: %w", file, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, file); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("застосовано міграцію", "file", file)
	}
	return nil
}
