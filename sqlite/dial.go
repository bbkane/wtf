package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/benbjohnson/wtf"
)

// findDialByID is a helper function to retrieve a dial by ID.
// Returns ENOTFOUND if dial doesn't exist.
func findDialByID(ctx context.Context, tx *Tx, id int) (*wtf.Dial, error) {
	dials, _, err := findDials(ctx, tx, wtf.DialFilter{ID: &id})
	if err != nil {
		return nil, err
	} else if len(dials) == 0 {
		return nil, &wtf.Error{Code: wtf.ENOTFOUND, Message: "Dial not found."}
	}
	return dials[0], nil
}

// checkDialExists returns nil if a dial does not exist. Otherwise returns ENOTFOUND.
// This is used to avoid permissions checks when inserting related objects.
//
// Unfortunately, SQLite provides poor FOREIGN KEY error descriptions but
// otherwise we would just use those.
func checkDialExists(ctx context.Context, tx *Tx, id int) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM dials WHERE id = ?`, id).Scan(&n); err != nil {
		return FormatError(err)
	} else if n == 0 {
		return &wtf.Error{Code: wtf.ENOTFOUND, Message: "Dial not found."}
	}
	return nil
}

// findDials retrieves a list of matching dials. Also returns a total matching
// count which may different from the number of results if filter.Limit is set.
func findDials(ctx context.Context, tx *Tx, filter wtf.DialFilter) (_ []*wtf.Dial, n int, err error) {
	// Build WHERE clause. Each part of the WHERE clause is AND-ed together.
	// Values are appended to an arg list to avoid SQL injection.
	where, args := []string{"1 = 1"}, []interface{}{}
	if v := filter.ID; v != nil {
		where, args = append(where, "id = ?"), append(args, *v)
	}

	// Limit to dials user is a member of unless searching by invite code.
	if v := filter.InviteCode; v != nil {
		where, args = append(where, "invite_code = ?"), append(args, *v)
	} else {
		userID := wtf.UserIDFromContext(ctx)
		where = append(where, `(
			id IN (SELECT dial_id FROM dial_memberships dm WHERE dm.user_id = ?)
		)`)
		args = append(args, userID)
	}

	// Execue query with limiting WHERE clause and LIMIT/OFFSET injected.
	rows, err := tx.QueryContext(ctx, `
		SELECT
		    id,
		    user_id,
		    name,
		    value,
		    invite_code,
		    created_at,
		    updated_at,
		    COUNT(*) OVER()
		FROM dials
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id ASC
		`+FormatLimitOffset(filter.Limit, filter.Offset),
		args...,
	)
	if err != nil {
		return nil, n, FormatError(err)
	}
	defer rows.Close()

	// Iterate over rows and deserialize into Dial objects.
	dials := make([]*wtf.Dial, 0)
	for rows.Next() {
		var dial wtf.Dial
		if err := rows.Scan(
			&dial.ID,
			&dial.UserID,
			&dial.Name,
			&dial.Value,
			&dial.InviteCode,
			(*NullTime)(&dial.CreatedAt),
			(*NullTime)(&dial.UpdatedAt),
			&n,
		); err != nil {
			return nil, 0, err
		}
		dials = append(dials, &dial)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return dials, n, nil
}

// refreshDialValue recomputes the WTF level of a dial by ID and saves it in dials.value.
func refreshDialValue(ctx context.Context, tx *Tx, id int) error {
	// Fetch current dial value.
	var oldValue int
	if err := tx.QueryRowContext(ctx, `SELECT value FROM dials WHERE id = ? `, id).Scan(&oldValue); err == sql.ErrNoRows {
		return nil // no dial, skip
	} else if err != nil {
		return FormatError(err)
	}

	// Compute average value from dial memberships.
	var newValue int
	if err := tx.QueryRowContext(ctx, `
		SELECT CAST(ROUND(IFNULL(AVG(value), 0)) AS INTEGER)
		FROM dial_memberships
		WHERE dial_id = ?
	`,
		id,
	).Scan(
		&newValue,
	); err != nil && err != sql.ErrNoRows {
		return FormatError(err)
	}

	// Exit if the value will not change.
	if oldValue == newValue {
		return nil
	}

	// Update value on dial.
	if _, err := tx.ExecContext(ctx, `
		UPDATE dials
		SET value = ?,
		    updated_at = ?
		WHERE id = ?
	`,
		newValue,
		(*NullTime)(&tx.now),
		id,
	); err != nil {
		return FormatError(err)
	}

	// Record historical value into "dial_values" table.
	if err := insertDialValue(ctx, tx, id, newValue, tx.now); err != nil {
		return fmt.Errorf("insert historical value: %w", err)
	}

	// Publish event to notify other members that the value has changed.
	if err := publishDialEvent(ctx, tx, id, wtf.Event{
		Type: wtf.EventTypeDialValueChanged,
		Payload: &wtf.DialValueChangedPayload{
			ID:    id,
			Value: newValue,
		},
	}); err != nil {
		return fmt.Errorf("publish dial event: %w", err)
	}

	return nil
}

// insertDialValue records a dial value at specific point in time.
func insertDialValue(ctx context.Context, tx *Tx, id int, value int, timestamp time.Time) error {
	// Reduce our precision to only one update per minute.
	timestamp = timestamp.Truncate(1 * time.Minute)

	// Insert a new record or update an existing record for the dial at the given timestamp.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dial_values (dial_id, "timestamp", value)
		VALUES (?, ?, ?)
		ON CONFLICT (dial_id, "timestamp") DO UPDATE SET value = ?
	`,
		id, (*NullTime)(&timestamp), value, value,
	); err != nil {
		return FormatError(err)
	}
	return nil
}

// publishDialEvent publishes event to the dial members.
func publishDialEvent(ctx context.Context, tx *Tx, id int, event wtf.Event) error {
	// Find all users who are members of the dial.
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM dial_memberships WHERE dial_id = ?`, id)
	if err != nil {
		return FormatError(err)
	}
	defer rows.Close()

	// Iterate over users and publish event.
	for rows.Next() {
		var userID int
		if err := rows.Scan(&userID); err != nil {
			return err
		}
		tx.db.EventService.PublishEvent(userID, event)
	}

	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}
