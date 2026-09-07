package service

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type salesServiceRepoStub struct {
	SalesRepository
	settings SalesSettings
	partner  SalesPartner
	saved    bool
}

func (r *salesServiceRepoStub) GetSettings(context.Context) (*SalesSettings, error) {
	return &r.settings, nil
}
func (r *salesServiceRepoStub) SaveSettings(_ context.Context, v *SalesSettings, _ int64) error {
	r.settings = *v
	r.saved = true
	return nil
}
func (r *salesServiceRepoStub) GetPartnerByCode(_ context.Context, code string) (*SalesPartner, error) {
	if code != r.partner.Code {
		return nil, ErrSalesNotFound
	}
	p := r.partner
	return &p, nil
}

func TestSalesReferralSignatureAndPromotionStatus(t *testing.T) {
	repo := &salesServiceRepoStub{settings: SalesSettings{Enabled: true, MainFrontendURL: "https://main.example.com"}, partner: SalesPartner{ID: 7, Code: "sales-a", PromotionEnabled: true}}
	s := NewSalesService(repo, &config.Config{JWT: config.JWTConfig{Secret: strings.Repeat("s", 32)}})
	token, target, err := s.IssueReferral(context.Background(), "SALES-A")
	require.NoError(t, err)
	require.Equal(t, "https://main.example.com/register", target)
	a, err := s.ResolveReferral(context.Background(), token)
	require.NoError(t, err)
	require.EqualValues(t, 7, a.PartnerID)
	parts := strings.Split(token, ".")
	parts[0] = "x" + parts[0]
	_, err = s.ResolveReferral(context.Background(), strings.Join(parts, "."))
	require.ErrorIs(t, err, ErrSalesInvalid)
	repo.partner.PromotionEnabled = false
	_, err = s.ResolveReferral(context.Background(), token)
	require.ErrorIs(t, err, ErrSalesDisabled)
	repo.partner.PromotionEnabled = true
	repo.settings.Enabled = false
	_, err = s.ResolveReferral(context.Background(), token)
	require.ErrorIs(t, err, ErrSalesDisabled)
}

func TestSalesSettingsRejectRedirectInjection(t *testing.T) {
	for _, raw := range []string{"https://user:pass@main.example.com", "https://main.example.com/other", "https://main.example.com?next=bad", "https://main.example.com#bad", "//main.example.com", "http://main.example.com"} {
		repo := &salesServiceRepoStub{}
		s := NewSalesService(repo, nil)
		require.ErrorIs(t, s.SaveSettings(context.Background(), &SalesSettings{Enabled: true, MainFrontendURL: raw}, 1), ErrSalesInvalid, raw)
		require.False(t, repo.saved)
	}
	repo := &salesServiceRepoStub{}
	s := NewSalesService(repo, nil)
	require.NoError(t, s.SaveSettings(context.Background(), &SalesSettings{Enabled: true, MainFrontendURL: "https://main.example.com/"}, 1))
	require.Equal(t, "https://main.example.com", repo.settings.MainFrontendURL)
}

func TestSalesPartnerAndSettlementValidation(t *testing.T) {
	valid := SalesPartnerInput{UserID: 1, Name: "Partner", Code: "sales-a", Hostname: "sales.example.com", CommissionRate: 25}
	require.NoError(t, validateSalesPartner(&valid))
	for _, rate := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		v := valid
		v.CommissionRate = rate
		require.ErrorIs(t, validateSalesPartner(&v), ErrSalesInvalid)
	}
	for _, host := range []string{"sales.example.com/path", "*.example.com", "a..example.com", "-a.example.com", "example.com", "sales.example.com:443"} {
		v := valid
		v.Hostname = host
		require.ErrorIs(t, validateSalesPartner(&v), ErrSalesInvalid, host)
	}
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	cutoff, err := SalesSettlementCutoff("2026-08", now)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), cutoff)
	_, err = SalesSettlementCutoff("2026-09", now)
	require.ErrorIs(t, err, ErrSalesInvalid)
	_, err = SalesSettlementCutoff("2026-13", now)
	require.ErrorIs(t, err, ErrSalesInvalid)
}

type salesWorkerRepoStub struct {
	SalesRepository
	started chan struct{}
	once    sync.Once
}

func (r *salesWorkerRepoStub) ProcessEvents(ctx context.Context, _ int) (int64, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestSalesWorkerStopsInFlightProcessing(t *testing.T) {
	repo := &salesWorkerRepoStub{started: make(chan struct{})}
	s := NewSalesService(repo, nil)
	s.Start()
	s.Start()
	select {
	case <-repo.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not cancel in-flight processing")
	}
	s.Stop()
}
