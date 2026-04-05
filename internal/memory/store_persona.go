package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// EnsurePersonaLayer seeds a persona layer if it does not exist.
func (s *Store) EnsurePersonaLayer(ctx context.Context, layer, content, source string) error {
	_, err := s.GetPersonaLayer(ctx, layer)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := s.now().UTC()
	_, err = s.writer.ExecContext(ctx, `
		INSERT INTO persona_profiles (layer, revision, content, updated_at)
		VALUES (?, 1, ?, ?)
	`,
		layer,
		content,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}

	return s.insertPersonaRevision(ctx, layer, 1, content, "seed", source)
}

// GetPersonaLayer returns the current persona layer snapshot.
func (s *Store) GetPersonaLayer(ctx context.Context, layer string) (PersonaProfile, error) {
	var profile PersonaProfile
	var updatedAt string
	err := s.writer.QueryRowContext(ctx, `
		SELECT layer, revision, content, updated_at
		FROM persona_profiles
		WHERE layer = ?
	`,
		layer,
	).Scan(&profile.Layer, &profile.Revision, &profile.Content, &updatedAt)
	if err != nil {
		return PersonaProfile{}, err
	}

	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return PersonaProfile{}, err
	}

	return profile, nil
}

// UpdatePersonaLayer writes a new revision if the content changed.
func (s *Store) UpdatePersonaLayer(ctx context.Context, layer, content, reason, source string) (PersonaProfile, error) {
	current, err := s.GetPersonaLayer(ctx, layer)
	if err != nil {
		return PersonaProfile{}, err
	}

	if strings.TrimSpace(current.Content) == strings.TrimSpace(content) {
		return current, nil
	}

	nextRevision := current.Revision + 1
	now := s.now().UTC()
	if _, err := s.writer.ExecContext(ctx, `
		UPDATE persona_profiles
		SET revision = ?, content = ?, updated_at = ?
		WHERE layer = ?
	`,
		nextRevision,
		content,
		now.Format(time.RFC3339Nano),
		layer,
	); err != nil {
		return PersonaProfile{}, err
	}

	if err := s.insertPersonaRevision(ctx, layer, nextRevision, content, reason, source); err != nil {
		return PersonaProfile{}, err
	}

	return PersonaProfile{
		Layer:     layer,
		Revision:  nextRevision,
		Content:   content,
		UpdatedAt: now,
	}, nil
}

func (s *Store) insertPersonaRevision(ctx context.Context, layer string, revision int, content, reason, source string) error {
	_, err := s.writer.ExecContext(ctx, `
		INSERT INTO persona_revisions (layer, revision, content, reason, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		layer,
		revision,
		content,
		reason,
		source,
		s.now().UTC().Format(time.RFC3339Nano),
	)

	return err
}
