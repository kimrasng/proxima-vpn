package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// ProviderGrant marks an order an administrator issued directly, the way a
// subscription voucher is handed out. It goes through the same order and
// settlement path as a purchase, so the user sees it in their order history
// and receipt instead of the plan silently changing under them.
const ProviderGrant = "grant"

var (
	ErrGrantInvalid      = errors.New("invalid plan grant")
	ErrGrantPlanNotFound = errors.New("plan not found")
	ErrGrantUserNotFound = errors.New("user not found")
	// ErrGrantPendingOrder: the user has an order awaiting payment. Granting
	// on top of it would leave two competing orders for one term.
	ErrGrantPendingOrder = errors.New("user has a pending order")
)

// PlanGrantRequest is one administrator grant of an existing plan.
type PlanGrantRequest struct {
	UserID       string
	PlanID       string
	DurationDays int
	// PriceCents is what the user paid outside the panel (bank transfer,
	// cash, a partner deal); zero records a complimentary grant.
	PriceCents int64
	// Note is the reason shown on the receipt, e.g. "October event".
	Note       string
	ActorID    string
	ActorLabel string
	RequestIP  string
	UserAgent  string
	RequestID  string
}

// PlanGrantService issues plans to users as recorded, receipted orders.
type PlanGrantService struct {
	db       *pgxpool.Pool
	payments *PaymentService
}

// NewPlanGrantService creates a PlanGrantService.
func NewPlanGrantService(db *pgxpool.Pool) *PlanGrantService {
	return &PlanGrantService{db: db, payments: NewPaymentService(db)}
}

// Grant creates an order for the plan and settles it, which grants or extends
// the plan by the same rules as a purchase. It returns the new order id.
func (s *PlanGrantService) Grant(ctx context.Context, req PlanGrantRequest) (string, *SettleResult, error) {
	if req.DurationDays <= 0 || req.DurationDays > 3650 || req.PriceCents < 0 {
		return "", nil, ErrGrantInvalid
	}
	var planOK bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plans WHERE id = $1)`, req.PlanID).Scan(&planOK); err != nil {
		return "", nil, fmt.Errorf("check plan: %w", err)
	}
	if !planOK {
		return "", nil, ErrGrantPlanNotFound
	}

	// provider_session_id carries the grant note: an admin grant has no
	// provider session, and the receipt reads this column already.
	var orderID string
	err := s.db.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, provider,
		                          provider_session_id, expires_at, client_ip, user_agent)
		 SELECT u.id, $2, $3, $4, 'pending', $5, $6, NOW() + INTERVAL '10 minutes', $7, $8
		 FROM users u WHERE u.id = $1
		 RETURNING id::text`,
		req.UserID, req.PlanID, req.DurationDays, req.PriceCents, ProviderGrant,
		TruncateUTF8(req.Note, 200), req.RequestIP, TruncateUTF8(req.UserAgent, MaxUserAgentBytes),
	).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrGrantUserNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", nil, ErrGrantPendingOrder
	}
	if err != nil {
		return "", nil, fmt.Errorf("create grant order: %w", err)
	}

	res, err := s.payments.Settle(ctx, payments.Confirmation{
		Provider:    ProviderGrant,
		ExternalID:  "grant:" + orderID,
		OrderID:     orderID,
		AmountCents: req.PriceCents,
		RequestIP:   req.RequestIP,
		UserAgent:   req.UserAgent,
		RequestID:   req.RequestID,
		ActorType:   "admin",
		ActorID:     req.ActorID,
		ActorLabel:  req.ActorLabel,
	})
	if err != nil || res.Outcome != SettleGranted {
		// Never leave a half-made grant waiting for a payment that will not come.
		_, _ = s.db.Exec(context.WithoutCancel(ctx),
			`UPDATE plan_orders SET status = 'cancelled', cancelled_at = NOW() WHERE id = $1 AND status = 'pending'`, orderID)
		if err != nil {
			return "", nil, fmt.Errorf("settle grant: %w", err)
		}
		return "", res, fmt.Errorf("settle grant: %s", res.Reason)
	}
	return orderID, res, nil
}
