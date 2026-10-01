package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ClaudeLimits — останні відомі ліміти підписки Claude.
// Info — rate_limit_info з події claude як є: адмінка сама бере з нього
// unifiedWindows (five_hour, seven_day, ...) — так нові вікна лімітів
// з'являться без змін у Go.
type ClaudeLimits struct {
	Info       json.RawMessage `json:"info"`
	ObservedAt time.Time       `json:"observed_at"` // коли claude їх повідомив
}

// SaveClaudeLimits запам'ятовує свіжі ліміти (замінює попередні).
func (s *Store) SaveClaudeLimits(ctx context.Context, info json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO claude_limits (id, info, observed_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET info = EXCLUDED.info, observed_at = EXCLUDED.observed_at`,
		info)
	return err
}

// ClaudeLimits повертає останні відомі ліміти (nil — claude ще жодного разу їх не повідомив).
func (s *Store) ClaudeLimits(ctx context.Context) (*ClaudeLimits, error) {
	var l ClaudeLimits
	err := s.pool.QueryRow(ctx, `SELECT info, observed_at FROM claude_limits WHERE id = 1`).Scan(&l.Info, &l.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}
