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

// RoleSetting — налаштування однієї ролі з адмін-панелі.
type RoleSetting struct {
	Role string `json:"role"`
	// ResumeSession: коли задача повертається до ролі, продовжувати її сесію
	// (claude --resume), а не починати нову розмову.
	ResumeSession bool `json:"resume_session"`
}

// RoleSettings повертає налаштування для ролей roles (у тому ж порядку).
// Ролі, яких немає в базі, отримують значення за замовчуванням: продовжувати сесію.
func (s *Store) RoleSettings(ctx context.Context, roles []string) ([]RoleSetting, error) {
	rows, err := s.pool.Query(ctx, `SELECT role, resume_session FROM role_settings`)
	if err != nil {
		return nil, err
	}
	saved := map[string]bool{}
	var (
		role   string
		resume bool
	)
	// ForEachRow викликає функцію для кожного рядка, підставивши колонки в role і resume.
	if _, err := pgx.ForEachRow(rows, []any{&role, &resume}, func() error {
		saved[role] = resume
		return nil
	}); err != nil {
		return nil, err
	}

	settings := make([]RoleSetting, 0, len(roles))
	for _, r := range roles {
		resume, ok := saved[r]
		settings = append(settings, RoleSetting{Role: r, ResumeSession: resume || !ok})
	}
	return settings, nil
}

// SaveRoleSettings зберігає налаштування ролей.
func (s *Store) SaveRoleSettings(ctx context.Context, settings []RoleSetting) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, st := range settings {
			_, err := tx.Exec(ctx, `
				INSERT INTO role_settings (role, resume_session) VALUES ($1, $2)
				ON CONFLICT (role) DO UPDATE SET resume_session = EXCLUDED.resume_session, updated_at = now()`,
				st.Role, st.ResumeSession)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
