package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/ramisoul84/kfc-loyalty/internal/domain"
)

// ErrDuplicate is returned when an idempotent write was already applied.
var ErrDuplicate = errors.New("already applied")

// Repository persists members, the points ledger, holds and kiosk codes.
//
// Every operation that changes a balance locks the member row
// (SELECT … FOR UPDATE) and writes the ledger entry and the new balance
// in the same transaction, so concurrent checkouts can't overspend and the
// balance always matches the ledger.
type Repository interface {
	// Members
	UpsertMemberByPhone(ctx context.Context, phone, firstName string) (*domain.Member, bool, error)
	GetMember(ctx context.Context, id uuid.UUID) (*domain.Member, error)
	UpdateMember(ctx context.Context, id uuid.UUID, firstName, email *string) (*domain.Member, error)
	ListMembers(ctx context.Context, search string, limit, offset int) ([]*domain.Member, int, error)

	// Ledger
	Credit(ctx context.Context, c *domain.Credit) (*domain.Entry, error)
	ListEntries(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.Entry, int, error)
	FindEntry(ctx context.Context, orderID uuid.UUID, t domain.EntryType) (*domain.Entry, error)

	// Holds
	Reserve(ctx context.Context, memberID uuid.UUID, points int, reference string, ttl time.Duration) (*domain.Hold, error)
	GetHold(ctx context.Context, id uuid.UUID) (*domain.Hold, error)
	CommitHold(ctx context.Context, id uuid.UUID, order domain.OrderInfo) (*domain.Hold, error)
	ReleaseHold(ctx context.Context, id uuid.UUID) (*domain.Hold, error)

	// Kiosk codes
	ReplaceCode(ctx context.Context, memberID uuid.UUID, codeHash string, expiresAt time.Time) error
	ConsumeCode(ctx context.Context, codeHash string) (uuid.UUID, error)
}

type repo struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) Repository {
	return &repo{db: db}
}

// memberSelect computes points_available from active holds.
const memberSelect = `
	SELECT m.id, m.phone, m.first_name, m.email, m.points_balance, m.lifetime_points,
	       m.created_at, m.updated_at,
	       m.points_balance - COALESCE((
	           SELECT SUM(h.points) FROM holds h
	           WHERE h.member_id = m.id AND h.status = 'held' AND h.expires_at > NOW()
	       ), 0)::INT AS points_available
	FROM members m`

// ═══════════════════════════════════════════════════════════════════
// MEMBERS
// ═══════════════════════════════════════════════════════════════════

func (r *repo) UpsertMemberByPhone(ctx context.Context, phone, firstName string) (*domain.Member, bool, error) {
	var name *string
	if strings.TrimSpace(firstName) != "" {
		name = &firstName
	}

	// xmax = 0 only for a freshly inserted row: tells us whether we created it.
	var id uuid.UUID
	var created bool
	err := r.db.QueryRowxContext(ctx, `
		INSERT INTO members (phone, first_name) VALUES ($1, $2)
		ON CONFLICT (phone) DO UPDATE SET updated_at = NOW()
		RETURNING id, (xmax = 0) AS created`, phone, name,
	).Scan(&id, &created)
	if err != nil {
		return nil, false, domain.NewInternalError(fmt.Errorf("upsert member: %w", err))
	}

	m, err := r.GetMember(ctx, id)
	return m, created, err
}

func (r *repo) GetMember(ctx context.Context, id uuid.UUID) (*domain.Member, error) {
	var m domain.Member
	err := r.db.GetContext(ctx, &m, memberSelect+` WHERE m.id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFoundError("member not found")
	}
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("get member: %w", err))
	}
	return &m, nil
}

func (r *repo) UpdateMember(ctx context.Context, id uuid.UUID, firstName, email *string) (*domain.Member, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE members
		SET first_name = COALESCE($2, first_name),
		    email      = COALESCE($3, email),
		    updated_at = NOW()
		WHERE id = $1`, id, firstName, email)
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("update member: %w", err))
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, domain.NewNotFoundError("member not found")
	}
	return r.GetMember(ctx, id)
}

func (r *repo) ListMembers(ctx context.Context, search string, limit, offset int) ([]*domain.Member, int, error) {
	where, args := "", []any{}
	if s := strings.TrimSpace(search); s != "" {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, s)
		args = append(args, "%"+s+"%")
		cond := "m.first_name ILIKE $1 OR m.email ILIKE $1"
		if digits != "" {
			args = append(args, "%"+digits+"%")
			cond += " OR m.phone LIKE $2"
		}
		where = " WHERE " + cond
	}

	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM members m`+where, args...); err != nil {
		return nil, 0, domain.NewInternalError(fmt.Errorf("count members: %w", err))
	}

	if limit <= 0 || limit > 100 {
		limit = 25
	}
	args = append(args, limit, max(offset, 0))
	query := fmt.Sprintf(`%s%s ORDER BY m.created_at DESC LIMIT $%d OFFSET $%d`,
		memberSelect, where, len(args)-1, len(args))

	var members []*domain.Member
	if err := r.db.SelectContext(ctx, &members, query, args...); err != nil {
		return nil, 0, domain.NewInternalError(fmt.Errorf("list members: %w", err))
	}
	return members, total, nil
}

// ═══════════════════════════════════════════════════════════════════
// LEDGER
// ═══════════════════════════════════════════════════════════════════

func (r *repo) Credit(ctx context.Context, c *domain.Credit) (*domain.Entry, error) {
	var entry *domain.Entry
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		var err error
		entry, err = credit(ctx, tx, c)
		return err
	})
	return entry, err
}

// credit writes one ledger entry and moves the balance. Caller holds a tx.
func credit(ctx context.Context, tx *sqlx.Tx, c *domain.Credit) (*domain.Entry, error) {
	var balance int
	err := tx.GetContext(ctx, &balance, `SELECT points_balance FROM members WHERE id = $1 FOR UPDATE`, c.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFoundError("member not found")
	}
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("lock member: %w", err))
	}

	// Idempotency check under the member lock (all writes for a member are
	// serialised by it). Checking first, rather than relying on the unique
	// index, keeps the surrounding transaction usable for the caller.
	if c.OrderID != nil || c.Type == domain.EntryWelcome {
		var exists bool
		err := tx.GetContext(ctx, &exists, `
			SELECT EXISTS (
				SELECT 1 FROM ledger
				WHERE ($1::UUID IS NOT NULL AND order_id = $1 AND type = $2)
				   OR ($2 = 'welcome' AND member_id = $3 AND type = 'welcome')
			)`, c.OrderID, c.Type, c.MemberID)
		if err != nil {
			return nil, domain.NewInternalError(fmt.Errorf("check duplicate: %w", err))
		}
		if exists {
			return nil, ErrDuplicate
		}
	}

	newBalance := balance + c.Points
	if newBalance < 0 && !c.AllowNegative {
		return nil, domain.NewConflictError("not enough points")
	}

	entry := &domain.Entry{
		ID:           uuid.New(),
		MemberID:     c.MemberID,
		Type:         c.Type,
		Points:       c.Points,
		BalanceAfter: newBalance,
		OrderID:      c.OrderID,
		RestaurantID: c.RestaurantID,
		HoldID:       c.HoldID,
	}
	if c.OrderNumber > 0 {
		entry.OrderNumber = &c.OrderNumber
	}
	if c.Description != "" {
		entry.Description = &c.Description
	}
	if c.Actor != "" {
		entry.Actor = &c.Actor
	}

	err = tx.QueryRowxContext(ctx, `
		INSERT INTO ledger (id, member_id, type, points, balance_after, order_id, order_number,
		                    restaurant_id, hold_id, description, actor)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING created_at`,
		entry.ID, entry.MemberID, entry.Type, entry.Points, entry.BalanceAfter, entry.OrderID,
		entry.OrderNumber, entry.RestaurantID, entry.HoldID, entry.Description, entry.Actor,
	).Scan(&entry.CreatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, domain.NewInternalError(fmt.Errorf("insert ledger: %w", err))
	}

	lifetime := 0
	if c.Points > 0 && (c.Type == domain.EntryEarn || c.Type == domain.EntryWelcome) {
		lifetime = c.Points
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE members SET points_balance = $2, lifetime_points = lifetime_points + $3, updated_at = NOW()
		WHERE id = $1`, c.MemberID, newBalance, lifetime); err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("update balance: %w", err))
	}
	return entry, nil
}

func (r *repo) ListEntries(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.Entry, int, error) {
	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM ledger WHERE member_id = $1`, memberID); err != nil {
		return nil, 0, domain.NewInternalError(fmt.Errorf("count ledger: %w", err))
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var entries []*domain.Entry
	err := r.db.SelectContext(ctx, &entries, `
		SELECT id, member_id, type, points, balance_after, order_id, order_number, restaurant_id,
		       hold_id, description, actor, created_at
		FROM ledger WHERE member_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, memberID, limit, max(offset, 0))
	if err != nil {
		return nil, 0, domain.NewInternalError(fmt.Errorf("list ledger: %w", err))
	}
	return entries, total, nil
}

func (r *repo) FindEntry(ctx context.Context, orderID uuid.UUID, t domain.EntryType) (*domain.Entry, error) {
	var e domain.Entry
	err := r.db.GetContext(ctx, &e, `
		SELECT id, member_id, type, points, balance_after, order_id, order_number, restaurant_id,
		       hold_id, description, actor, created_at
		FROM ledger WHERE order_id = $1 AND type = $2`, orderID, t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFoundError("ledger entry not found")
	}
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("find ledger entry: %w", err))
	}
	return &e, nil
}

// ═══════════════════════════════════════════════════════════════════
// HOLDS
// ═══════════════════════════════════════════════════════════════════

const holdColumns = `id, member_id, points, reference, status, order_id, expires_at, created_at, updated_at`

func (r *repo) Reserve(
	ctx context.Context,
	memberID uuid.UUID,
	points int,
	reference string,
	ttl time.Duration,
) (*domain.Hold, error) {
	var hold domain.Hold
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		// Serialise all checkouts of this member.
		var balance int
		err := tx.GetContext(ctx, &balance, `SELECT points_balance FROM members WHERE id = $1 FOR UPDATE`, memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFoundError("member not found")
		}
		if err != nil {
			return domain.NewInternalError(fmt.Errorf("lock member: %w", err))
		}

		// Idempotent retry: same reference → same hold.
		err = tx.GetContext(ctx, &hold, `SELECT `+holdColumns+` FROM holds WHERE member_id = $1 AND reference = $2`,
			memberID, reference)
		if err == nil {
			if hold.Points != points {
				return domain.NewConflictError("reference already used for a different amount")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.NewInternalError(fmt.Errorf("find hold: %w", err))
		}

		var held int
		if err := tx.GetContext(ctx, &held, `
			SELECT COALESCE(SUM(points), 0) FROM holds
			WHERE member_id = $1 AND status = 'held' AND expires_at > NOW()`, memberID); err != nil {
			return domain.NewInternalError(fmt.Errorf("sum holds: %w", err))
		}
		if balance-held < points {
			return domain.NewConflictError("not enough points")
		}

		return tx.GetContext(ctx, &hold, `
			INSERT INTO holds (member_id, points, reference, expires_at)
			VALUES ($1, $2, $3, NOW() + $4::INTERVAL)
			RETURNING `+holdColumns,
			memberID, points, reference, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	})
	if err != nil {
		return nil, err
	}
	return &hold, nil
}

func (r *repo) GetHold(ctx context.Context, id uuid.UUID) (*domain.Hold, error) {
	var h domain.Hold
	err := r.db.GetContext(ctx, &h, `SELECT `+holdColumns+` FROM holds WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFoundError("hold not found")
	}
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("get hold: %w", err))
	}
	return &h, nil
}

// CommitHold turns a hold into a redeem entry for the order.
//
// A released or expired hold can still be committed: the order exists
// with the discount applied, so the points must be charged. That case can
// take the balance below zero; it is logged by the service.
func (r *repo) CommitHold(ctx context.Context, id uuid.UUID, order domain.OrderInfo) (*domain.Hold, error) {
	var hold domain.Hold
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		err := tx.GetContext(ctx, &hold, `SELECT `+holdColumns+` FROM holds WHERE id = $1 FOR UPDATE`, id)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFoundError("hold not found")
		}
		if err != nil {
			return domain.NewInternalError(fmt.Errorf("lock hold: %w", err))
		}
		if hold.Status == domain.HoldCommitted {
			return nil // idempotent
		}

		_, err = credit(ctx, tx, &domain.Credit{
			MemberID:      hold.MemberID,
			Type:          domain.EntryRedeem,
			Points:        -hold.Points,
			OrderID:       &order.ID,
			OrderNumber:   order.Number,
			RestaurantID:  &order.RestaurantID,
			HoldID:        &hold.ID,
			Description:   fmt.Sprintf("Discount on order #%03d", order.Number),
			AllowNegative: true,
		})
		if err != nil && !errors.Is(err, ErrDuplicate) {
			return err
		}

		return tx.GetContext(ctx, &hold, `
			UPDATE holds SET status = 'committed', order_id = $2, updated_at = NOW()
			WHERE id = $1 RETURNING `+holdColumns, id, order.ID)
	})
	if err != nil {
		return nil, err
	}
	return &hold, nil
}

func (r *repo) ReleaseHold(ctx context.Context, id uuid.UUID) (*domain.Hold, error) {
	var hold domain.Hold
	err := r.db.GetContext(ctx, &hold, `
		UPDATE holds SET status = 'released', updated_at = NOW()
		WHERE id = $1 AND status = 'held'
		RETURNING `+holdColumns, id)
	if errors.Is(err, sql.ErrNoRows) {
		// Already committed or released: return it as is.
		return r.GetHold(ctx, id)
	}
	if err != nil {
		return nil, domain.NewInternalError(fmt.Errorf("release hold: %w", err))
	}
	return &hold, nil
}

// ═══════════════════════════════════════════════════════════════════
// KIOSK CODES
// ═══════════════════════════════════════════════════════════════════

// ReplaceCode stores a new code for the member and drops any older one,
// so only the code currently on the customer's screen works.
func (r *repo) ReplaceCode(ctx context.Context, memberID uuid.UUID, codeHash string, expiresAt time.Time) error {
	return r.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM member_codes WHERE member_id = $1 OR expires_at < NOW() - INTERVAL '1 day'`, memberID); err != nil {
			return domain.NewInternalError(fmt.Errorf("delete old codes: %w", err))
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO member_codes (code_hash, member_id, expires_at) VALUES ($1, $2, $3)`,
			codeHash, memberID, expiresAt)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				return ErrDuplicate // astronomically rare collision; caller retries
			}
			return domain.NewInternalError(fmt.Errorf("insert code: %w", err))
		}
		return nil
	})
}

// ConsumeCode marks a valid, unused code as used and returns its member.
func (r *repo) ConsumeCode(ctx context.Context, codeHash string) (uuid.UUID, error) {
	var memberID uuid.UUID
	err := r.db.GetContext(ctx, &memberID, `
		UPDATE member_codes SET used_at = NOW()
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING member_id`, codeHash)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, domain.NewNotFoundError("code is invalid or expired")
	}
	if err != nil {
		return uuid.Nil, domain.NewInternalError(fmt.Errorf("consume code: %w", err))
	}
	return memberID, nil
}

// ═══════════════════════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════════════════════

func (r *repo) inTx(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return domain.NewInternalError(fmt.Errorf("begin: %w", err))
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return domain.NewInternalError(fmt.Errorf("commit: %w", err))
	}
	return nil
}
