package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/ramisoul84/kfc-loyalty/internal/domain"
	"github.com/ramisoul84/kfc-loyalty/internal/service"
	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
)

// orderEvent is the subset of kfc-order's orders.events message that
// loyalty needs.
type orderEvent struct {
	Type  string `json:"type"`
	Order struct {
		ID            uuid.UUID  `json:"id"`
		RestaurantID  uuid.UUID  `json:"restaurant_id"`
		Number        int        `json:"number"`
		Status        string     `json:"status"`
		Total         float64    `json:"total"` // rubles, after discount
		MemberID      *uuid.UUID `json:"member_id,omitempty"`
		LoyaltyHoldID *uuid.UUID `json:"loyalty_hold_id,omitempty"`
	} `json:"order"`
}

// OrderConsumer turns order events into points:
//
//	order.created   with a hold   → commit the hold (reconciles a commit
//	                                the gateway couldn't confirm)
//	status=completed with member  → earn points on the amount paid
//	status=cancelled              → refund redeemed points / release hold
//
// Every action is idempotent, so redelivered events are harmless.
type OrderConsumer struct {
	svc    service.Service
	logger *logger.Logger
}

func NewOrderConsumer(svc service.Service, log *logger.Logger) *OrderConsumer {
	return &OrderConsumer{svc: svc, logger: log}
}

func (c *OrderConsumer) Handle(ctx context.Context, _ string, value []byte) error {
	var evt orderEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		return fmt.Errorf("unmarshal order event: %w", err)
	}
	o := evt.Order
	if o.MemberID == nil && o.LoyaltyHoldID == nil {
		return nil // not a loyalty order
	}
	info := domain.OrderInfo{ID: o.ID, Number: o.Number, RestaurantID: o.RestaurantID}

	switch {
	case evt.Type == "order.created" && o.LoyaltyHoldID != nil:
		_, err := c.svc.Commit(ctx, *o.LoyaltyHoldID, info)
		return err

	case o.Status == "completed" && o.MemberID != nil:
		paid := int64(math.Round(o.Total * 100))
		return c.svc.EarnForOrder(ctx, *o.MemberID, info, paid)

	case o.Status == "cancelled":
		return c.svc.RefundForOrder(ctx, info, o.LoyaltyHoldID)
	}
	return nil
}
