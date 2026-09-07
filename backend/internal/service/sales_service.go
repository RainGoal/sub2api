package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SalesReferralCookieName = "sales_referral"
const SalesSettingKey = "custom_sales_config"

var (
	ErrSalesDisabled      = infraerrors.Forbidden("SALES_DISABLED", "sales program is disabled")
	ErrSalesNotFound      = infraerrors.NotFound("SALES_NOT_FOUND", "sales record not found")
	ErrSalesConflict      = infraerrors.Conflict("SALES_CONFLICT", "sales operation conflicts with existing state")
	ErrSalesInvalid       = infraerrors.BadRequest("SALES_INVALID", "invalid sales configuration or request")
	ErrSalesPendingEvents = infraerrors.Conflict("SALES_PENDING_EVENTS", "unprocessed commission events prevent settlement")
	ErrSalesPayoutFrozen  = infraerrors.Conflict("SALES_PAYOUT_FROZEN", "partner payouts are frozen")
)

type SalesSettings struct {
	Enabled         bool   `json:"enabled"`
	MainFrontendURL string `json:"main_frontend_url"`
}

type SalesPartner struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	Name             string    `json:"name"`
	Code             string    `json:"code"`
	Hostname         string    `json:"hostname"`
	CommissionRate   float64   `json:"commission_rate"`
	PromotionEnabled bool      `json:"promotion_enabled"`
	AccrualEnabled   bool      `json:"accrual_enabled"`
	PayoutFrozen     bool      `json:"payout_frozen"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type SalesPartnerInput struct {
	UserID           int64   `json:"user_id"`
	Name             string  `json:"name"`
	Code             string  `json:"code"`
	Hostname         string  `json:"hostname"`
	CommissionRate   float64 `json:"commission_rate"`
	PromotionEnabled bool    `json:"promotion_enabled"`
	AccrualEnabled   bool    `json:"accrual_enabled"`
	PayoutFrozen     bool    `json:"payout_frozen"`
}

type SalesAttribution struct {
	PartnerID int64  `json:"partner_id"`
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

type SalesFilter struct {
	PartnerID int64
	Page      int
	PageSize  int
	Search    string
	StartAt   *time.Time
	EndAt     *time.Time
}

type SalesCustomer struct {
	UserID     int64     `json:"user_id"`
	PartnerID  int64     `json:"partner_id"`
	Email      string    `json:"email"`
	CreatedAt  time.Time `json:"created_at"`
	Revenue    float64   `json:"revenue"`
	Profit     float64   `json:"profit"`
	Commission float64   `json:"commission"`
}

type SalesLedgerEntry struct {
	ID             int64     `json:"id"`
	PartnerID      int64     `json:"partner_id"`
	CustomerUserID *int64    `json:"customer_user_id,omitempty"`
	EventID        *int64    `json:"event_id,omitempty"`
	Kind           string    `json:"kind"`
	Revenue        float64   `json:"revenue"`
	Cost           float64   `json:"cost"`
	Profit         float64   `json:"profit"`
	CommissionRate float64   `json:"commission_rate"`
	Commission     float64   `json:"commission"`
	Currency       string    `json:"currency"`
	Note           string    `json:"note"`
	OccurredAt     time.Time `json:"occurred_at"`
	SettlementID   *int64    `json:"settlement_id,omitempty"`
}

type SalesOverview struct {
	Partner             *SalesPartner `json:"partner"`
	CustomerCount       int64         `json:"customer_count"`
	Revenue             float64       `json:"revenue"`
	Cost                float64       `json:"cost"`
	Profit              float64       `json:"profit"`
	Commission          float64       `json:"commission"`
	UnsettledCommission float64       `json:"unsettled_commission"`
	PendingPayout       float64       `json:"pending_payout"`
	PaidCommission      float64       `json:"paid_commission"`
	PendingEvents       int64         `json:"pending_events"`
}

type SalesSettlement struct {
	ID               int64      `json:"id"`
	PartnerID        int64      `json:"partner_id"`
	Month            string     `json:"month"`
	Cutoff           time.Time  `json:"cutoff"`
	NetCommission    float64    `json:"net_commission"`
	PayoutAmount     float64    `json:"payout_amount"`
	CarryAmount      float64    `json:"carry_amount"`
	Status           string     `json:"status"`
	Currency         string     `json:"currency"`
	PaymentReference string     `json:"payment_reference"`
	CreatedAt        time.Time  `json:"created_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	PaidAt           *time.Time `json:"paid_at,omitempty"`
}

type SalesSettlementInput struct {
	PartnerID  int64  `json:"partner_id"`
	Month      string `json:"month"`
	RequestKey string `json:"request_key"`
}

type SalesPaymentInput struct {
	RequestKey       string `json:"request_key"`
	PaymentReference string `json:"payment_reference"`
}

type SalesAdjustmentInput struct {
	PartnerID      int64   `json:"partner_id"`
	SourceLedgerID *int64  `json:"source_ledger_id"`
	Commission     float64 `json:"commission"`
	Note           string  `json:"note"`
	RequestKey     string  `json:"request_key"`
}

type SalesRepository interface {
	GetSettings(context.Context) (*SalesSettings, error)
	SaveSettings(context.Context, *SalesSettings, int64) error
	GetPartner(context.Context, int64) (*SalesPartner, error)
	GetPartnerByUser(context.Context, int64) (*SalesPartner, error)
	GetPartnerByCode(context.Context, string) (*SalesPartner, error)
	ListPartners(context.Context, SalesFilter) ([]SalesPartner, int64, error)
	SavePartner(context.Context, int64, SalesPartnerInput, int64) (*SalesPartner, error)
	BindCustomer(context.Context, int64, *SalesAttribution) error
	RollbackRegistration(context.Context, int64) error
	ListCustomers(context.Context, SalesFilter) ([]SalesCustomer, int64, error)
	ListLedger(context.Context, SalesFilter) ([]SalesLedgerEntry, int64, error)
	Overview(context.Context, int64, SalesFilter) (*SalesOverview, error)
	ListSettlements(context.Context, SalesFilter) ([]SalesSettlement, int64, error)
	CreateSettlement(context.Context, SalesSettlementInput, time.Time, int64) (*SalesSettlement, error)
	ConfirmSettlement(context.Context, int64, string, int64) (*SalesSettlement, error)
	PaySettlement(context.Context, int64, SalesPaymentInput, int64) (*SalesSettlement, error)
	Adjust(context.Context, SalesAdjustmentInput, int64) (*SalesLedgerEntry, error)
	ProcessEvents(context.Context, int) (int64, error)
}

type SalesService struct {
	repo   SalesRepository
	secret []byte
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewSalesService(repo SalesRepository, cfg *config.Config) *SalesService {
	s := &SalesService{repo: repo}
	if cfg != nil {
		s.secret = []byte("sales-referral-v1:" + cfg.JWT.Secret)
	}
	return s
}

func (s *SalesService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	done := s.done
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			workCtx, workCancel := context.WithTimeout(ctx, 20*time.Second)
			var err error
			for workCtx.Err() == nil {
				var processed int64
				processed, err = s.repo.ProcessEvents(workCtx, 250)
				if err != nil || processed == 0 {
					break
				}
			}
			workCancel()
			if err != nil && ctx.Err() == nil {
				slog.Error("sales commission event processing failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *SalesService) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	s.mu.Lock()
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
}

func (s *SalesService) GetSettings(ctx context.Context) (*SalesSettings, error) {
	return s.repo.GetSettings(ctx)
}

// RollbackRegistration is restricted to the incomplete OAuth signup rollback.
// Normal account deletion never removes ownership or financial history.
func (s *SalesService) RollbackRegistration(ctx context.Context, userID int64) error {
	return s.repo.RollbackRegistration(ctx, userID)
}

func (s *SalesService) SaveSettings(ctx context.Context, cfg *SalesSettings, actorID int64) error {
	if cfg == nil {
		return ErrSalesInvalid
	}
	cfg.MainFrontendURL = strings.TrimRight(strings.TrimSpace(cfg.MainFrontendURL), "/")
	if cfg.MainFrontendURL != "" {
		u, err := url.Parse(cfg.MainFrontendURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return ErrSalesInvalid
		}
	}
	if cfg.Enabled && cfg.MainFrontendURL == "" {
		return ErrSalesInvalid
	}
	return s.repo.SaveSettings(ctx, cfg, actorID)
}

func (s *SalesService) enabled(ctx context.Context) error {
	cfg, err := s.repo.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return ErrSalesDisabled
	}
	return nil
}

// IssueReferral signs server-validated attribution. The signature uses the existing JWT
// secret with a distinct purpose prefix, never an exposed or client supplied key.
func (s *SalesService) IssueReferral(ctx context.Context, code string) (string, string, error) {
	a, err := s.ResolveReferral(ctx, code)
	if err != nil {
		return "", "", err
	}
	if a == nil || len(s.secret) < 32 {
		return "", "", ErrSalesInvalid
	}
	cfg, err := s.repo.GetSettings(ctx)
	if err != nil {
		return "", "", err
	}
	a.ExpiresAt = time.Now().Add(30 * 24 * time.Hour).Unix()
	payload, _ := json.Marshal(a)
	data := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(data))
	return data + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), cfg.MainFrontendURL + "/register", nil
}

// ResolveReferral accepts an explicit promotion code or a signed referral cookie.
// Callers must only use the cookie on signup; existing users are never rebound.
func (s *SalesService) ResolveReferral(ctx context.Context, value string) (*SalesAttribution, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if err := s.enabled(ctx); err != nil {
		return nil, err
	}
	var a *SalesAttribution
	code := strings.ToLower(value)
	if strings.Contains(value, ".") {
		parts := strings.Split(value, ".")
		if len(parts) != 2 || len(value) > 1024 || len(s.secret) < 32 {
			return nil, ErrSalesInvalid
		}
		provided, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, ErrSalesInvalid
		}
		mac := hmac.New(sha256.New, s.secret)
		_, _ = mac.Write([]byte(parts[0]))
		if !hmac.Equal(provided, mac.Sum(nil)) {
			return nil, ErrSalesInvalid
		}
		data, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil || json.Unmarshal(data, &a) != nil || a == nil || a.ExpiresAt <= time.Now().Unix() {
			return nil, ErrSalesInvalid
		}
		code = a.Code
	}
	p, err := s.repo.GetPartnerByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if !p.PromotionEnabled {
		return nil, ErrSalesDisabled
	}
	if a != nil && a.PartnerID != p.ID {
		return nil, ErrSalesInvalid
	}
	return &SalesAttribution{PartnerID: p.ID, Code: p.Code, ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix()}, nil
}

func (s *SalesService) BindCustomer(ctx context.Context, userID int64, a *SalesAttribution) error {
	if a == nil {
		return nil
	}
	if err := s.enabled(ctx); err != nil {
		return err
	}
	if a.PartnerID <= 0 || userID <= 0 || a.ExpiresAt <= time.Now().Unix() {
		return ErrSalesInvalid
	}
	return s.repo.BindCustomer(ctx, userID, a)
}

var salesCodeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,47}$`)
var salesHostLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validateSalesPartner(in *SalesPartnerInput) error {
	in.Name, in.Code, in.Hostname = strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Code)), strings.ToLower(strings.TrimSpace(in.Hostname))
	if in.UserID <= 0 || in.Name == "" || len(in.Name) > 100 || !salesCodeRE.MatchString(in.Code) || math.IsNaN(in.CommissionRate) || math.IsInf(in.CommissionRate, 0) || in.CommissionRate < 0 || in.CommissionRate > 100 || len(in.Hostname) > 253 {
		return ErrSalesInvalid
	}
	labels := strings.Split(in.Hostname, ".")
	if len(labels) < 3 {
		return ErrSalesInvalid
	}
	for _, label := range labels {
		if !salesHostLabelRE.MatchString(label) {
			return ErrSalesInvalid
		}
	}
	in.CommissionRate = QuantizeUsageBillingAmount(in.CommissionRate)
	return nil
}

func NormalizeSalesFilter(f SalesFilter) SalesFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	f.Search = strings.TrimSpace(f.Search)
	return f
}

func (s *SalesService) ListPartners(ctx context.Context, f SalesFilter) ([]SalesPartner, int64, error) {
	return s.repo.ListPartners(ctx, NormalizeSalesFilter(f))
}
func (s *SalesService) SavePartner(ctx context.Context, id int64, in SalesPartnerInput, actorID int64) (*SalesPartner, error) {
	if err := validateSalesPartner(&in); err != nil {
		return nil, err
	}
	return s.repo.SavePartner(ctx, id, in, actorID)
}
func (s *SalesService) ListCustomers(ctx context.Context, f SalesFilter) ([]SalesCustomer, int64, error) {
	return s.repo.ListCustomers(ctx, NormalizeSalesFilter(f))
}
func (s *SalesService) ListLedger(ctx context.Context, f SalesFilter) ([]SalesLedgerEntry, int64, error) {
	return s.repo.ListLedger(ctx, NormalizeSalesFilter(f))
}
func (s *SalesService) ListSettlements(ctx context.Context, f SalesFilter) ([]SalesSettlement, int64, error) {
	return s.repo.ListSettlements(ctx, NormalizeSalesFilter(f))
}

func (s *SalesService) SelfPartner(ctx context.Context, userID int64) (*SalesPartner, error) {
	return s.repo.GetPartnerByUser(ctx, userID)
}
func (s *SalesService) Overview(ctx context.Context, partnerID int64, f SalesFilter) (*SalesOverview, error) {
	return s.repo.Overview(ctx, partnerID, f)
}

func SalesSettlementCutoff(month string, now time.Time) (time.Time, error) {
	t, err := time.Parse("2006-01", month)
	if err != nil || t.Year() < 1970 {
		return time.Time{}, ErrSalesInvalid
	}
	cutoff := t.AddDate(0, 1, 0)
	if cutoff.After(now.UTC()) {
		return time.Time{}, ErrSalesInvalid
	}
	return cutoff, nil
}

func salesRequestKeyValid(key string) bool {
	return len(strings.TrimSpace(key)) >= 8 && len(key) <= 128 && key == strings.TrimSpace(key)
}

func (s *SalesService) CreateSettlement(ctx context.Context, in SalesSettlementInput, actor int64) (*SalesSettlement, error) {
	if in.PartnerID <= 0 || !salesRequestKeyValid(in.RequestKey) {
		return nil, ErrSalesInvalid
	}
	cutoff, err := SalesSettlementCutoff(in.Month, time.Now())
	if err != nil {
		return nil, err
	}
	return s.repo.CreateSettlement(ctx, in, cutoff, actor)
}
func (s *SalesService) ConfirmSettlement(ctx context.Context, id int64, key string, actor int64) (*SalesSettlement, error) {
	if id <= 0 || !salesRequestKeyValid(key) {
		return nil, ErrSalesInvalid
	}
	return s.repo.ConfirmSettlement(ctx, id, key, actor)
}
func (s *SalesService) PaySettlement(ctx context.Context, id int64, in SalesPaymentInput, actor int64) (*SalesSettlement, error) {
	in.PaymentReference = strings.TrimSpace(in.PaymentReference)
	if id <= 0 || !salesRequestKeyValid(in.RequestKey) || in.PaymentReference == "" || len(in.PaymentReference) > 255 {
		return nil, ErrSalesInvalid
	}
	return s.repo.PaySettlement(ctx, id, in, actor)
}
func (s *SalesService) Adjust(ctx context.Context, in SalesAdjustmentInput, actor int64) (*SalesLedgerEntry, error) {
	in.Note = strings.TrimSpace(in.Note)
	if in.PartnerID <= 0 || !salesRequestKeyValid(in.RequestKey) || in.Note == "" || len(in.Note) > 500 || math.IsNaN(in.Commission) || math.IsInf(in.Commission, 0) || math.Abs(in.Commission) > 1e10 {
		return nil, ErrSalesInvalid
	}
	in.Commission = QuantizeUsageBillingAmount(in.Commission)
	if in.Commission == 0 {
		return nil, ErrSalesInvalid
	}
	return s.repo.Adjust(ctx, in, actor)
}

func IsSalesAttributionUnavailable(err error) bool {
	return errors.Is(err, ErrSalesDisabled) || errors.Is(err, ErrSalesNotFound) || errors.Is(err, ErrSalesInvalid)
}
