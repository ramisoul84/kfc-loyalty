# kfc-loyalty

Loyalty program for the KFC platform: members, points, and redeeming
points as a discount on the website and at kiosks.

## Rules (configurable)

| Rule | Default | Env |
|---|---|---|
| Points earned | 5 per 100 ₽ paid (after discount) | `LOYALTY_EARN_PERCENT` |
| Point value | 1 point = 1 ₽ | `LOYALTY_POINT_VALUE_KOPECKS` |
| Max discount | 50% of the order subtotal | `LOYALTY_MAX_REDEEM_PERCENT` |
| Welcome bonus | 100 points | `LOYALTY_WELCOME_BONUS` |
| Checkout hold | 15 min | `LOYALTY_HOLD_TTL` |
| Kiosk code lifetime | 5 min, single use | `LOYALTY_CODE_TTL` |

Points are earned when the order is **handed over** (`completed`), so
cancelled orders never earn.

## Design

```
kfc-userapi ──gRPC──▶┌──────────────┐──▶ Postgres (kfc_loyalty)
kfc-crm     ──gRPC──▶│ kfc-loyalty  │
                     └──────▲───────┘
                            │ Kafka orders.events (consumer group kfc-loyalty)
                        kfc-order
```

- **Ledger, not a counter.** Every change is an append-only `ledger` row
  (`welcome`, `earn`, `redeem`, `refund`, `adjust`) with the balance after
  it. `members.points_balance` is updated in the same transaction with the
  member row locked, so it always equals the ledger sum and concurrent
  checkouts can't overspend.
- **Idempotent by order.** A unique `(order_id, type)` means a replayed
  event or retried call can't earn, redeem or refund twice.
- **Two-phase redemption.**
  1. `ReservePoints(member, points, reference, subtotal)` places a *hold*.
     Held points stop counting as available. Same reference → same hold.
  2. The gateway creates the order with the discount and the hold id.
  3. `CommitHold` turns the hold into a `redeem` entry. If the gateway
     never gets to confirm, the `order.created` event commits it anyway.
  4. If order creation fails, `ReleaseHold`. If nobody does, the hold
     expires and the points are available again.
  5. A cancelled order refunds its redeemed points (`refund` entry).
- **Kiosk codes.** `IssueMemberCode` returns an 8-digit, single-use code
  valid for 5 minutes; issuing a new one voids the old. Only a SHA-256 of
  the code is stored. The website shows it as a QR code and as digits;
  userapi rate-limits failed lookups per kiosk.

## gRPC API (`proto/loyalty/v1/loyalty.proto`)

| RPC | Caller |
|---|---|
| `GetProgram` | userapi (rules shown to customers) |
| `UpsertMemberByPhone`, `GetMember`, `UpdateMember`, `ListTransactions` | userapi |
| `IssueMemberCode`, `ResolveMemberCode` | userapi (website, kiosk) |
| `ReservePoints`, `CommitHold`, `ReleaseHold` | userapi (checkout) |
| `ListMembers`, `AdjustPoints` | CRM |

Customer authentication (SMS code, tokens) lives in kfc-userapi; this
service trusts its callers and only knows members by id and phone.

## Running

```bash
cp .env.example .env
make run          # gRPC :9104, health :8004
```
