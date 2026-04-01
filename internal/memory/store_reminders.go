package memory

import (
	"context"
	"database/sql"
	"time"
)

// AddReminder creates a persisted reminder.
func (s *Store) AddReminder(ctx context.Context, input ReminderInput) (Reminder, error) {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO reminders (
			channel_id, thread_ts, message, due_at, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		input.ChannelID,
		input.ThreadTS,
		input.Message,
		input.DueAt.UTC().Format(time.RFC3339Nano),
		input.CreatedBy,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Reminder{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Reminder{}, err
	}

	return Reminder{
		ID:        id,
		ChannelID: input.ChannelID,
		ThreadTS:  input.ThreadTS,
		Message:   input.Message,
		DueAt:     input.DueAt.UTC(),
		CreatedBy: input.CreatedBy,
		CreatedAt: now,
	}, nil
}

// DueReminders returns reminders that should fire now.
func (s *Store) DueReminders(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, message, due_at, created_by, created_at, sent_at
		FROM reminders
		WHERE sent_at IS NULL
		  AND due_at <= ?
		ORDER BY due_at ASC
		LIMIT ?
	`,
		now.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reminders := make([]Reminder, 0, limit)
	for rows.Next() {
		reminder, scanErr := scanReminder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		reminders = append(reminders, reminder)
	}

	return reminders, rows.Err()
}

// MarkReminderSent marks a reminder as delivered.
func (s *Store) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE reminders
		SET sent_at = ?
		WHERE id = ?
	`,
		sentAt.UTC().Format(time.RFC3339Nano),
		reminderID,
	)

	return err
}

// PendingReminderCount returns the number of outstanding reminders.
func (s *Store) PendingReminderCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM reminders
		WHERE sent_at IS NULL
	`).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func scanReminder(scanner interface{ Scan(dest ...any) error }) (Reminder, error) {
	var reminder Reminder
	var dueAt string
	var createdAt string
	var sentAt sql.NullString
	if err := scanner.Scan(
		&reminder.ID,
		&reminder.ChannelID,
		&reminder.ThreadTS,
		&reminder.Message,
		&dueAt,
		&reminder.CreatedBy,
		&createdAt,
		&sentAt,
	); err != nil {
		return Reminder{}, err
	}

	var err error
	reminder.DueAt, err = time.Parse(time.RFC3339Nano, dueAt)
	if err != nil {
		return Reminder{}, err
	}
	reminder.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Reminder{}, err
	}
	if sentAt.Valid {
		parsed, parseErr := time.Parse(time.RFC3339Nano, sentAt.String)
		if parseErr != nil {
			return Reminder{}, parseErr
		}
		reminder.SentAt = &parsed
	}

	return reminder, nil
}
