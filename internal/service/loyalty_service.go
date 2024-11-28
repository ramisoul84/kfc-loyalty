package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ramisoul84/kfc-loyalty/internal/config"
	"github.com/ramisoul84/kfc-loyalty/internal/domain"
	"github.com/ramisoul84/kfc-loyalty/internal/repository"
	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
)

// Service implements the loyalty program.
type Service interface {
	Program() domain.Program

	UpsertMemberByPhone(ctx context.Context, phone, firstName string) (*domain.Member, bool, error)
	GetMember(ctx context.Context, id uuid.UUID) (*domain.Member, error)
	UpdateMember(ctx context.Context, id uuid.UUID, firstName, email *string) (*domain.Member, error)
	ListTransactions(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.Entry, int, error)

	IssueCode(ctx context.Context, memberID uuid.UUID) (string, time.Time, error)
	ResolveCode(ctx context.Context, code string) (*domain.Member, error)

	Reserve(ctx context.Context, memberID uuid.UUID, points int, reference string, orderSubtotal int64) (*domain.Hold, error)
	Commit(ctx context.Context, holdID uuid.UUID, order domain.OrderInfo) (*domain.Hold, error)
	Release(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error)

	ListMembers(ctx context.Context, search string, limit, offset int) ([]*domain.Member, int, error)
	Adjust(ctx context.Context, memberID uuid.UUID, points int, reason, actor string) (*domain.Member, error)

	// Order lifecycle (from Kafka).
	EarnForOrder(ctx context.Context, memberID uuid.UUID, order domain.OrderInfo, paidKopecks int64) error
	RefundForOrder(ctx context.Context, order domain.OrderInfo, holdID *uuid.UUID) error
}

type service struct {
	repo   repository.Repository
	cfg    config.ProgramConfig
	logger *logger.Logger
}

func New(repo repository.Repository, cfg config.ProgramConfig, log *logger.Logger) Service {
	return &service{repo: repo, cfg: cfg, logger: log}
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{9,14}$`)

const codeDigits = 8

func (s *service) Program() domain.Program {
	return domain.Program{
		EarnPercent:       s.cfg.EarnPercent,
		MaxRedeemPercent:  s.cfg.MaxRedeemPercent,
		WelcomeBonus:      s.cfg.WelcomeBonus,
		PointValueKopecks: s.cfg.PointValueKopecks,
	}
}

// ═══════════════════════════════════════════════════════════════════
// MEMBERS
// ═══════════════════════════════════════════════════════════════════

func (s *service) UpsertMemberByPhone(ctx context.Context, phone, firstName string) (*domain.Member, bool, error) {
	if !e164.MatchString(phone) {
		return nil, false, domain.NewValidationError("phone must be in international format, e.g. +79991234567")
	}
	firstName = strings.TrimSpace(firstName)
	if utf8.RuneCountInString(firstName) > 60 {
		return nil, false, domain.NewValidationError("name is too long")
	}

	m, created, err := s.repo.UpsertMemberByPhone(ctx, phone, firstName)
	if err != nil {
		return nil, false, err
	}
	if created {
		s.logger.Info("member joined", "member_id", m.ID)
		if s.cfg.WelcomeBonus > 0 {
			_, err := s.repo.Credit(ctx, &domain.Credit{
				MemberID:    m.ID,
				Type:        domain.EntryWelcome,
				Points:      s.cfg.WelcomeBonus,
				Description: "Welcome bonus",
			})
			if err != nil && !errors.Is(err, repository.ErrDuplicate) {
				// The account exists; a missing bonus can be granted from CRM.
				s.logger.Error("welcome bonus failed", "member_id", m.ID, "error", err)
			}
			if m, err = s.repo.GetMember(ctx, m.ID); err != nil {
				return nil, false, err
			}
		}
	}
	return m, created, nil
}

func (s *service) GetMember(ctx context.Context, id uuid.UUID) (*domain.Member, error) {
	return s.repo.GetMember(ctx, id)
}

func (s *service) UpdateMember(ctx context.Context, id uuid.UUID, firstName, email *string) (*domain.Member, error) {
	if firstName != nil {
		n := strings.TrimSpace(*firstName)
		if c := utf8.RuneCountInString(n); c < 1 || c > 60 {
			return nil, domain.NewValidationError("name must be 1–60 characters")
		}
		firstName = &n
	}
	if email != nil {
		e := strings.TrimSpace(*email)
		if e != "" {
			if _, err := mail.ParseAddress(e); err != nil || len(e) > 255 {
				return nil, domain.NewValidationError("invalid email address")
			}
		}
		email = &e
	}
	return s.repo.UpdateMember(ctx, id, firstName, email)
}

func (s *service) ListTransactions(ctx context.Context, memberID uuid.UUID, limit, offset int) ([]*domain.Entry, int, error) {
	return s.repo.ListEntries(ctx, memberID, limit, offset)
}

func (s *service) ListMembers(ctx context.Context, search string, limit, offset int) ([]*domain.Member, int, error) {
	return s.repo.ListMembers(ctx, search, limit, offset)
}

func (s *service) Adjust(ctx context.Context, memberID uuid.UUID, points int, reason, actor string) (*domain.Member, error) {
	reason = strings.TrimSpace(reason)
	if points == 0 {
		return nil, domain.NewValidationError("points must not be zero")
	}
	if points > 100_000 || points < -100_000 {
		return nil, domain.NewValidationError("adjustment is too large")
	}
	if utf8.RuneCountInString(reason) < 3 || utf8.RuneCountInString(reason) > 200 {
		return nil, domain.NewValidationError("a reason (3–200 characters) is required")
	}
	if _, err := s.repo.Credit(ctx, &domain.Credit{
		MemberID:    memberID,
		Type:        domain.EntryAdjust,
		Points:      points,
		Description: reason,
		Actor:       actor,
	}); err != nil {
		return nil, err
	}
	s.logger.Info("points adjusted", "member_id", memberID, "points", points, "actor", actor, "reason", reason)
	return s.repo.GetMember(ctx, memberID)
}

// ═══════════════════════════════════════════════════════════════════
// KIOSK CODES
// ═══════════════════════════════════════════════════════════════════

// IssueCode creates a fresh single-use code for the member's screen and
// invalidates the previous one.
func (s *service) IssueCode(ctx context.Context, memberID uuid.UUID) (string, time.Time, error) {
	if _, err := s.repo.GetMember(ctx, memberID); err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(s.cfg.CodeTTL)
	for attempt := 0; attempt < 5; attempt++ {
		code, err := randomDigits(codeDigits)
		if err != nil {
			return "", time.Time{}, domain.NewInternalError(err)
		}
		err = s.repo.ReplaceCode(ctx, memberID, hashCode(code), expiresAt)
		if errors.Is(err, repository.ErrDuplicate) {
			continue
		}
		if err != nil {
			return "", time.Time{}, err
		}
		return code, expiresAt, nil
	}
	return "", time.Time{}, domain.NewInternalError(errors.New("could not allocate a unique code"))
}

// ResolveCode exchanges a code for its member. Codes work once.
func (s *service) ResolveCode(ctx context.Context, code string) (*domain.Member, error) {
	code = strings.TrimSpace(code)
	if len(code) != codeDigits || strings.Trim(code, "0123456789") != "" {
		return nil, domain.NewNotFoundError("code is invalid or expired")
	}
	memberID, err := s.repo.ConsumeCode(ctx, hashCode(code))
	if err != nil {
		return nil, err
	}
	return s.repo.GetMember(ctx, memberID)
}

// ═══════════════════════════════════════════════════════════════════
// REDEMPTION
// ═══════════════════════════════════════════════════════════════════

// MaxRedeemable is the most points an order of this subtotal may use.
func (s *service) maxRedeemable(subtotal int64) int {
	return int(subtotal * int64(s.cfg.MaxRedeemPercent) / 100 / s.cfg.PointValueKopecks)
}

func (s *service) Reserve(
	ctx context.Context,
	memberID uuid.UUID,
	points int,
	reference string,
	orderSubtotal int64,
) (*domain.Hold, error) {
	if points <= 0 {
		return nil, domain.NewValidationError("points must be positive")
	}
	if reference == "" || len(reference) > 100 {
		return nil, domain.NewValidationError("a reference (≤100 chars) is required")
	}
	if orderSubtotal > 0 {
		if limit := s.maxRedeemable(orderSubtotal); points > limit {
			return nil, domain.NewValidationError(
				fmt.Sprintf("points can cover at most %d%% of the order (%d points)", s.cfg.MaxRedeemPercent, limit))
		}
	}

	hold, err := s.repo.Reserve(ctx, memberID, points, reference, s.cfg.HoldTTL)
	if err != nil {
		return nil, err
	}
	s.logger.Info("points reserved", "member_id", memberID, "hold_id", hold.ID, "points", points)
	return hold, nil
}

func (s *service) Commit(ctx context.Context, holdID uuid.UUID, order domain.OrderInfo) (*domain.Hold, error) {
	before, err := s.repo.GetHold(ctx, holdID)
	if err != nil {
		return nil, err
	}
	hold, err := s.repo.CommitHold(ctx, holdID, order)
	if err != nil {
		return nil, err
	}
	if before.Status != domain.HoldCommitted {
		if before.Status == domain.HoldReleased || before.ExpiresAt.Before(time.Now()) {
			s.logger.Warn("committed a released/expired hold; balance may go negative",
				"hold_id", holdID, "order_id", order.ID)
		}
		s.logger.Info("points redeemed", "member_id", hold.MemberID, "order_id", order.ID, "points", hold.Points)
	}
	return hold, nil
}

func (s *service) Release(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error) {
	hold, err := s.repo.ReleaseHold(ctx, holdID)
	if err != nil {
		return nil, err
	}
	s.logger.Info("hold released", "hold_id", holdID, "status", hold.Status)
	return hold, nil
}

// ═══════════════════════════════════════════════════════════════════
// ORDER LIFECYCLE
// ═══════════════════════════════════════════════════════════════════

// EarnForOrder credits points for a handed-over order. Idempotent.
func (s *service) EarnForOrder(ctx context.Context, memberID uuid.UUID, order domain.OrderInfo, paidKopecks int64) error {
	points := int(paidKopecks * int64(s.cfg.EarnPercent) / 100 / s.cfg.PointValueKopecks)
	if points <= 0 {
		return nil
	}
	_, err := s.repo.Credit(ctx, &domain.Credit{
		MemberID:     memberID,
		Type:         domain.EntryEarn,
		Points:       points,
		OrderID:      &order.ID,
		OrderNumber:  order.Number,
		RestaurantID: &order.RestaurantID,
		Description:  fmt.Sprintf("Order #%03d", order.Number),
	})
	if errors.Is(err, repository.ErrDuplicate) {
		return nil
	}
	if err != nil {
		return err
	}
	s.logger.Info("points earned", "member_id", memberID, "order_id", order.ID, "points", points)
	return nil
}

// RefundForOrder returns redeemed points when an order is cancelled, or
// releases its hold if the redemption was never committed. Idempotent.
func (s *service) RefundForOrder(ctx context.Context, order domain.OrderInfo, holdID *uuid.UUID) error {
	redeem, err := s.repo.FindEntry(ctx, order.ID, domain.EntryRedeem)
	if domain.IsNotFoundError(err) {
		if holdID != nil {
			_, err := s.repo.ReleaseHold(ctx, *holdID)
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}

	_, err = s.repo.Credit(ctx, &domain.Credit{
		MemberID:     redeem.MemberID,
		Type:         domain.EntryRefund,
		Points:       -redeem.Points,
		OrderID:      &order.ID,
		OrderNumber:  order.Number,
		RestaurantID: &order.RestaurantID,
		Description:  fmt.Sprintf("Order #%03d cancelled", order.Number),
	})
	if errors.Is(err, repository.ErrDuplicate) {
		return nil
	}
	if err != nil {
		return err
	}
	s.logger.Info("points refunded", "member_id", redeem.MemberID, "order_id", order.ID, "points", -redeem.Points)
	return nil
}

// ═══════════════════════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════════════════════

func randomDigits(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("random code: %w", err)
		}
		b[i] = byte('0' + d.Int64())
	}
	return string(b), nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
