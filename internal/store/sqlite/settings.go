package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nitro/forum_forge/internal/model"
)

const settingsKey = "settings"

func (s *Store) GetSettings(ctx context.Context) (*model.Settings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		"SELECT value FROM settings WHERE key = ?", settingsKey,
	).Scan(&raw)

	if err != nil {
		// No row yet — return defaults.
		defaults := model.DefaultSettings()
		return &defaults, nil
	}

	var settings model.Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("get settings unmarshal: %w", err)
	}
	return &settings, nil
}

func (s *Store) SaveSettings(ctx context.Context, settings *model.Settings) error {
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("save settings marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKey, string(data),
	)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
