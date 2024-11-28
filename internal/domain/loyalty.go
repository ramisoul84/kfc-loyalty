package domain

import (
	"time"

	"github.com/google/uuid"
)

// Ledger entry types.
type EntryType string

const (
	EntryWelcome EntryType = "welcome" // sign-up bonus
	EntryEarn    EntryType = "earn"    // points for a handed-over order
	EntryRedeem  EntryType = "redeem"  // points spent as a discount
	EntryRefund  EntryType = "refund"  // redeemed points returned (order cancelled)
	EntryAdjust  EntryType = "adjust"  // manual correction from CRM
)

// Hold statuses.
type HoldStatus string

const (
	HoldHeld      HoldStatus = "held"
	HoldCommitted HoldStatus = "committed"
	HoldReleased  HoldStatus = "released"
)

// Member is a loyalty customer.
type Member struct {
	ID             uuid.UUID `db:"id"`
	Phone          string    `db:"phone"`
	FirstName      *string   `db:"first_name"`
	Email          *string   `db:"email"`
	PointsBalance  int       `db:"points_balance"`
	LifetimePoints int       `db:"lifetime_points"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`

	// PointsAvailable = balance minus active holds. Filled by the repository.
	PointsAvailable int `db:"points_available"`
}

// Entry is one ledger row.
type Entry struct {
	ID           uuid.UUID  `db:"id"`
	MemberID     uuid.UUID  `db:"member_id"`
	Type         EntryType  `db:"type"`
	Points       int        `db:"points"`
	BalanceAfter int        `db:"balance_after"`
	OrderID      *uuid.UUID `db:"order_id"`
	OrderNumber  *int       `db:"order_number"`
	RestaurantID *uuid.UUID `db:"restaurant_id"`
	HoldID       *uuid.UUID `db:"hold_id"`
	Description  *string    `db:"description"`
	Actor        *string    `db:"actor"`
	CreatedAt    time.Time  `db:"created_at"`
}

// Credit describes a ledger write. Points is signed.
type Credit struct {
	MemberID     uuid.UUID
	Type         EntryType
	Points       int
	OrderID      *uuid.UUID
	OrderNumber  int
	RestaurantID *uuid.UUID
	HoldID       *uuid.UUID
	Description  string
	Actor        string
	// AllowNegative lets a debit take the balance below zero. Only for
	// committing a hold whose order already exists with the discount.
	AllowNegative bool
}

// Hold is a set of points reserved by an in-flight checkout.
type Hold struct {
	ID        uuid.UUID  `db:"id"`
	MemberID  uuid.UUID  `db:"member_id"`
	Points    int        `db:"points"`
	Reference string     `db:"reference"`
	Status    HoldStatus `db:"status"`
	OrderID   *uuid.UUID `db:"order_id"`
	ExpiresAt time.Time  `db:"expires_at"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
}

// Program is the set of loyalty rules exposed to clients.
type Program struct {
	EarnPercent       int
	MaxRedeemPercent  int
	WelcomeBonus      int
	PointValueKopecks int64
}

// OrderInfo identifies the order a ledger entry belongs to.
type OrderInfo struct {
	ID           uuid.UUID
	Number       int
	RestaurantID uuid.UUID
}
