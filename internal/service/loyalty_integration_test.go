package service

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/ramisoul84/kfc-loyalty/internal/config"
	"github.com/ramisoul84/kfc-loyalty/internal/domain"
	"github.com/ramisoul84/kfc-loyalty/internal/repository"
	"github.com/ramisoul84/kfc-loyalty/migrations"
	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
	"github.com/ramisoul84/kfc-loyalty/pkg/migrate"
)

// Integration tests against a real Postgres, because the guarantees that
// matter (no overspending under concurrency, idempotency per order) live
// in transactions and locks. Run with:
//
//	TEST_DATABASE_URL=postgres://user:pass@localhost:5432/kfc_loyalty_test?sslmode=disable go test ./...
func newTestService(t *testing.T) (Service, *sqlx.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Up(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`TRUNCATE member_codes, holds, ledger, members CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	cfg := config.ProgramConfig{
		EarnPercent: 5, MaxRedeemPercent: 50, WelcomeBonus: 100,
		PointValueKopecks: 100, HoldTTL: 15 * time.Minute, CodeTTL: 5 * time.Minute,
	}
	log := logger.New(&logger.Config{Level: "error", Format: "text", Output: "stdout", Service: "test"})
	return New(repository.New(db), cfg, log), db
}

func newMember(t *testing.T, svc Service) *domain.Member {
	t.Helper()
	phone := fmt.Sprintf("+7999%07d", rand.IntN(10_000_000))
	m, created, err := svc.UpsertMemberByPhone(context.Background(), phone, "Test")
	if err != nil || !created {
		t.Fatalf("create member: created=%v err=%v", created, err)
	}
	return m
}

func order() domain.OrderInfo {
	return domain.OrderInfo{ID: uuid.New(), Number: 1, RestaurantID: uuid.New()}
}

// ledgerSum must always equal the stored balance.
func assertBalance(t *testing.T, db *sqlx.DB, memberID uuid.UUID, want int) {
	t.Helper()
	var balance, sum int
	if err := db.Get(&balance, `SELECT points_balance FROM members WHERE id = $1`, memberID); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&sum, `SELECT COALESCE(SUM(points), 0) FROM ledger WHERE member_id = $1`, memberID); err != nil {
		t.Fatal(err)
	}
	if balance != want || sum != want {
		t.Fatalf("balance=%d ledger=%d, want %d", balance, sum, want)
	}
}

func TestWelcomeBonusOnlyOnce(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)
	_, created, err := svc.UpsertMemberByPhone(ctx, m.Phone, "")
	if err != nil || created {
		t.Fatalf("second upsert: created=%v err=%v", created, err)
	}
	assertBalance(t, db, m.ID, 100)
}

// Ten checkouts race for 30 points each with 100 available: exactly three
// may win. Holds count against the balance before anything is committed.
func TestConcurrentReservationsCannotOverspend(t *testing.T) {
	svc, db := newTestService(t)
	m := newMember(t, svc)

	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Reserve(context.Background(), m.ID, 30, uuid.NewString(), 100_000)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				won++
			} else if !domain.IsConflictError(err) {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if won != 3 {
		t.Fatalf("%d reservations succeeded, want 3", won)
	}
	got, _ := svc.GetMember(context.Background(), m.ID)
	if got.PointsAvailable != 10 {
		t.Fatalf("available=%d, want 10", got.PointsAvailable)
	}
	assertBalance(t, db, m.ID, 100) // nothing committed yet
}

func TestReserveIsIdempotentPerReference(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)

	h1, err := svc.Reserve(ctx, m.ID, 40, "checkout-1", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := svc.Reserve(ctx, m.ID, 40, "checkout-1", 100_000)
	if err != nil || h2.ID != h1.ID {
		t.Fatalf("retry should return the same hold: %v %v", h2, err)
	}
	if _, err := svc.Reserve(ctx, m.ID, 50, "checkout-1", 100_000); !domain.IsConflictError(err) {
		t.Fatalf("same reference with another amount must conflict, got %v", err)
	}
}

func TestReserveRespectsMaxShareOfOrder(t *testing.T) {
	svc, _ := newTestService(t)
	m := newMember(t, svc)
	// 50% of a 120 ₽ order is 60 points.
	if _, err := svc.Reserve(context.Background(), m.ID, 61, uuid.NewString(), 12_000); !domain.IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if _, err := svc.Reserve(context.Background(), m.ID, 60, uuid.NewString(), 12_000); err != nil {
		t.Fatalf("60 points should be allowed: %v", err)
	}
}

// Kafka redelivers: commit, earn and refund must each apply once.
func TestCommitEarnRefundAreIdempotent(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)
	o := order()

	h, err := svc.Reserve(ctx, m.ID, 60, uuid.NewString(), 100_000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.Commit(ctx, h.ID, o); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	assertBalance(t, db, m.ID, 40)

	for i := 0; i < 3; i++ {
		if err := svc.EarnForOrder(ctx, m.ID, o, 50_000); err != nil { // 500 ₽ → 25 pts
			t.Fatal(err)
		}
	}
	assertBalance(t, db, m.ID, 65)

	o2 := order()
	h2, _ := svc.Reserve(ctx, m.ID, 20, uuid.NewString(), 100_000)
	_, _ = svc.Commit(ctx, h2.ID, o2)
	for i := 0; i < 3; i++ {
		if err := svc.RefundForOrder(ctx, o2, &h2.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertBalance(t, db, m.ID, 65)
}

// The gateway may give up on a checkout (release) while the order was in
// fact created; the order.created event then commits the hold anyway.
func TestCommitAfterReleaseStillCharges(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)

	h, _ := svc.Reserve(ctx, m.ID, 30, uuid.NewString(), 100_000)
	if _, err := svc.Release(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commit(ctx, h.ID, order()); err != nil {
		t.Fatal(err)
	}
	assertBalance(t, db, m.ID, 70)
}

func TestCancelledUncommittedOrderReleasesHold(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)

	h, _ := svc.Reserve(ctx, m.ID, 50, uuid.NewString(), 100_000)
	if err := svc.RefundForOrder(ctx, order(), &h.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetMember(ctx, m.ID)
	if got.PointsAvailable != 100 {
		t.Fatalf("hold not released: available=%d", got.PointsAvailable)
	}
}

func TestKioskCodeIsSingleUseAndReplaced(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	m := newMember(t, svc)

	old, _, err := svc.IssueCode(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, _ := svc.IssueCode(ctx, m.ID)
	if _, err := svc.ResolveCode(ctx, old); !domain.IsNotFoundError(err) {
		t.Fatalf("a replaced code must not work, got %v", err)
	}
	if got, err := svc.ResolveCode(ctx, fresh); err != nil || got.ID != m.ID {
		t.Fatalf("fresh code: %v %v", got, err)
	}
	if _, err := svc.ResolveCode(ctx, fresh); !domain.IsNotFoundError(err) {
		t.Fatalf("a used code must not work twice, got %v", err)
	}
}

func TestAdjustCannotGoNegative(t *testing.T) {
	svc, db := newTestService(t)
	m := newMember(t, svc)
	_, err := svc.Adjust(context.Background(), m.ID, -150, "correction", "user:test")
	if !domain.IsConflictError(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	assertBalance(t, db, m.ID, 100)
}
