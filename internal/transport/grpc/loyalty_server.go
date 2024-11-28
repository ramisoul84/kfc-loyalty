package grpc

import (
	"context"

	"github.com/google/uuid"

	loyaltyv1 "github.com/ramisoul84/kfc-loyalty/gen/loyalty/v1"
	"github.com/ramisoul84/kfc-loyalty/internal/domain"
	"github.com/ramisoul84/kfc-loyalty/internal/service"
)

// LoyaltyServer implements loyaltyv1.LoyaltyServiceServer.
type LoyaltyServer struct {
	loyaltyv1.UnimplementedLoyaltyServiceServer
	svc service.Service
}

func NewLoyaltyServer(svc service.Service) *LoyaltyServer {
	return &LoyaltyServer{svc: svc}
}

func (s *LoyaltyServer) GetProgram(context.Context, *loyaltyv1.GetProgramRequest) (*loyaltyv1.Program, error) {
	p := s.svc.Program()
	return &loyaltyv1.Program{
		EarnPercent:       int32(p.EarnPercent),
		MaxRedeemPercent:  int32(p.MaxRedeemPercent),
		WelcomeBonus:      int32(p.WelcomeBonus),
		PointValueKopecks: p.PointValueKopecks,
	}, nil
}

func (s *LoyaltyServer) UpsertMemberByPhone(ctx context.Context, req *loyaltyv1.UpsertMemberByPhoneRequest) (*loyaltyv1.UpsertMemberResponse, error) {
	m, created, err := s.svc.UpsertMemberByPhone(ctx, req.GetPhone(), req.GetFirstName())
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return &loyaltyv1.UpsertMemberResponse{Member: toProtoMember(m), Created: created}, nil
}

func (s *LoyaltyServer) GetMember(ctx context.Context, req *loyaltyv1.GetMemberRequest) (*loyaltyv1.Member, error) {
	id, err := parseID(req.GetId(), "member id")
	if err != nil {
		return nil, err
	}
	m, err := s.svc.GetMember(ctx, id)
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoMember(m), nil
}

func (s *LoyaltyServer) UpdateMember(ctx context.Context, req *loyaltyv1.UpdateMemberRequest) (*loyaltyv1.Member, error) {
	id, err := parseID(req.GetId(), "member id")
	if err != nil {
		return nil, err
	}
	m, err := s.svc.UpdateMember(ctx, id, optional(req.GetFirstName()), optional(req.GetEmail()))
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoMember(m), nil
}

func (s *LoyaltyServer) ListTransactions(ctx context.Context, req *loyaltyv1.ListTransactionsRequest) (*loyaltyv1.ListTransactionsResponse, error) {
	id, err := parseID(req.GetMemberId(), "member id")
	if err != nil {
		return nil, err
	}
	entries, total, err := s.svc.ListTransactions(ctx, id, int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	out := &loyaltyv1.ListTransactionsResponse{Total: int32(total)}
	for _, e := range entries {
		out.Transactions = append(out.Transactions, toProtoEntry(e))
	}
	return out, nil
}

func (s *LoyaltyServer) IssueMemberCode(ctx context.Context, req *loyaltyv1.IssueMemberCodeRequest) (*loyaltyv1.MemberCode, error) {
	id, err := parseID(req.GetMemberId(), "member id")
	if err != nil {
		return nil, err
	}
	code, expiresAt, err := s.svc.IssueCode(ctx, id)
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return &loyaltyv1.MemberCode{Code: code, ExpiresAt: expiresAt.UnixMilli()}, nil
}

func (s *LoyaltyServer) ResolveMemberCode(ctx context.Context, req *loyaltyv1.ResolveMemberCodeRequest) (*loyaltyv1.Member, error) {
	m, err := s.svc.ResolveCode(ctx, req.GetCode())
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoMember(m), nil
}

func (s *LoyaltyServer) ReservePoints(ctx context.Context, req *loyaltyv1.ReservePointsRequest) (*loyaltyv1.Hold, error) {
	id, err := parseID(req.GetMemberId(), "member id")
	if err != nil {
		return nil, err
	}
	h, err := s.svc.Reserve(ctx, id, int(req.GetPoints()), req.GetReference(), req.GetOrderSubtotal())
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoHold(h), nil
}

func (s *LoyaltyServer) CommitHold(ctx context.Context, req *loyaltyv1.CommitHoldRequest) (*loyaltyv1.Hold, error) {
	holdID, err := parseID(req.GetHoldId(), "hold id")
	if err != nil {
		return nil, err
	}
	orderID, err := parseID(req.GetOrderId(), "order id")
	if err != nil {
		return nil, err
	}
	restaurantID, err := parseID(req.GetRestaurantId(), "restaurant id")
	if err != nil {
		return nil, err
	}
	h, err := s.svc.Commit(ctx, holdID, domain.OrderInfo{
		ID: orderID, Number: int(req.GetOrderNumber()), RestaurantID: restaurantID,
	})
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoHold(h), nil
}

func (s *LoyaltyServer) ReleaseHold(ctx context.Context, req *loyaltyv1.ReleaseHoldRequest) (*loyaltyv1.Hold, error) {
	id, err := parseID(req.GetHoldId(), "hold id")
	if err != nil {
		return nil, err
	}
	h, err := s.svc.Release(ctx, id)
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoHold(h), nil
}

func (s *LoyaltyServer) ListMembers(ctx context.Context, req *loyaltyv1.ListMembersRequest) (*loyaltyv1.ListMembersResponse, error) {
	members, total, err := s.svc.ListMembers(ctx, req.GetSearch(), int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	out := &loyaltyv1.ListMembersResponse{Total: int32(total)}
	for _, m := range members {
		out.Members = append(out.Members, toProtoMember(m))
	}
	return out, nil
}

func (s *LoyaltyServer) AdjustPoints(ctx context.Context, req *loyaltyv1.AdjustPointsRequest) (*loyaltyv1.Member, error) {
	id, err := parseID(req.GetMemberId(), "member id")
	if err != nil {
		return nil, err
	}
	m, err := s.svc.Adjust(ctx, id, int(req.GetPoints()), req.GetReason(), req.GetActor())
	if err != nil {
		return nil, mapErrorToGRPC(err)
	}
	return toProtoMember(m), nil
}

// ── conversion ───────────────────────────────────────────────────

func toProtoMember(m *domain.Member) *loyaltyv1.Member {
	return &loyaltyv1.Member{
		Id:              m.ID.String(),
		Phone:           m.Phone,
		FirstName:       deref(m.FirstName),
		Email:           deref(m.Email),
		PointsBalance:   int32(m.PointsBalance),
		PointsAvailable: int32(max(m.PointsAvailable, 0)),
		LifetimePoints:  int32(m.LifetimePoints),
		CreatedAt:       m.CreatedAt.UnixMilli(),
	}
}

func toProtoEntry(e *domain.Entry) *loyaltyv1.Transaction {
	t := &loyaltyv1.Transaction{
		Id:           e.ID.String(),
		Type:         string(e.Type),
		Points:       int32(e.Points),
		BalanceAfter: int32(e.BalanceAfter),
		Description:  deref(e.Description),
		CreatedAt:    e.CreatedAt.UnixMilli(),
	}
	if e.OrderID != nil {
		t.OrderId = e.OrderID.String()
	}
	if e.OrderNumber != nil {
		t.OrderNumber = int32(*e.OrderNumber)
	}
	if e.RestaurantID != nil {
		t.RestaurantId = e.RestaurantID.String()
	}
	return t
}

func toProtoHold(h *domain.Hold) *loyaltyv1.Hold {
	p := &loyaltyv1.Hold{
		Id:        h.ID.String(),
		MemberId:  h.MemberID.String(),
		Points:    int32(h.Points),
		Status:    string(h.Status),
		ExpiresAt: h.ExpiresAt.UnixMilli(),
	}
	if h.OrderID != nil {
		p.OrderId = h.OrderID.String()
	}
	return p
}

func parseID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, mapErrorToGRPC(domain.NewValidationError("invalid " + field))
	}
	return id, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
