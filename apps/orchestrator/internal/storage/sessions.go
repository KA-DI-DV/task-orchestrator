package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Режими сесії агента в історії задачі (task_logs.session_mode).
const (
	SessionNew      = "new"      // нова сесія
	SessionResume   = "resume"   // задача повернулась до ролі — продовжив свою сесію
	SessionContinue = "continue" // відновив сесію, перервану зупинкою задачі
)

// RoleSession повертає останню сесію ролі в задачі ("" — роль ще не працювала).
func (s *Store) RoleSession(ctx context.Context, taskID, role string) (string, error) {
	var session string
	err := s.pool.QueryRow(ctx,
		`SELECT session_id FROM task_sessions WHERE task_id = $1 AND role = $2`, taskID, role,
	).Scan(&session)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return session, err
}

// SetRoleSession запам'ятовує сесію ролі в задачі (замінює попередню).
func (s *Store) SetRoleSession(ctx context.Context, taskID, role, session string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO task_sessions (task_id, role, session_id) VALUES ($1, $2, $3)
		ON CONFLICT (task_id, role) DO UPDATE SET session_id = EXCLUDED.session_id, updated_at = now()`,
		taskID, role, session)
	return err
}

// DefaultModel — модель ролі, поки її не змінили в адмін-панелі.
const DefaultModel = "claude-opus-5-5"

// Models — моделі, які можна обрати для ролі (повні id: короткі псевдоніми
// на кшталт "sonnet" у CLI можуть вести на попереднє покоління).
var Models = []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}

// RoleSetting — налаштування однієї ролі з адмін-панелі.
type RoleSetting struct {
	Role string `json:"role"`
	// ResumeSession: коли задача повертається до ролі, продовжувати її сесію
	// (claude --resume), а не починати нову розмову.
	ResumeSession bool `json:"resume_session"`
	// Model — з якою моделлю claude запускати агента цієї ролі.
	Model string `json:"model"`
}

// RoleSettings повертає налаштування для ролей roles (у тому ж порядку).
// Ролі, яких немає в базі, отримують значення за замовчуванням: продовжувати сесію.
func (s *Store) RoleSettings(ctx context.Context, roles []string) ([]RoleSetting, error) {
	rows, err := s.pool.Query(ctx, `SELECT role, resume_session, model FROM role_settings`)
	if err != nil {
		return nil, err
	}
	saved := map[string]RoleSetting{}
	var st RoleSetting
	// ForEachRow викликає функцію для кожного рядка, підставивши колонки в поля st.
	if _, err := pgx.ForEachRow(rows, []any{&st.Role, &st.ResumeSession, &st.Model}, func() error {
		saved[st.Role] = st
		return nil
	}); err != nil {
		return nil, err
	}

	settings := make([]RoleSetting, 0, len(roles))
	for _, r := range roles {
		st, ok := saved[r]
		if !ok {
			st = RoleSetting{Role: r, ResumeSession: true} // за замовчуванням — продовжувати сесію
		}
		if st.Model == "" {
			st.Model = DefaultModel
		}
		settings = append(settings, st)
	}
	return settings, nil
}

// SaveRoleSettings зберігає налаштування ролей.
func (s *Store) SaveRoleSettings(ctx context.Context, settings []RoleSetting) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, st := range settings {
			_, err := tx.Exec(ctx, `
				INSERT INTO role_settings (role, resume_session, model) VALUES ($1, $2, $3)
				ON CONFLICT (role) DO UPDATE SET
					resume_session = EXCLUDED.resume_session,
					model          = EXCLUDED.model,
					updated_at     = now()`,
				st.Role, st.ResumeSession, st.Model)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
