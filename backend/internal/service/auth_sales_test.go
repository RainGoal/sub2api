//go:build unit

package service_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type authSalesRepositoryStub struct {
	service.SalesRepository
	enabled      bool
	failBind     bool
	failRollback bool
	bindCalls    int
}

func (r *authSalesRepositoryStub) GetSettings(context.Context) (*service.SalesSettings, error) {
	return &service.SalesSettings{Enabled: r.enabled, MainFrontendURL: "https://main.example.com"}, nil
}

func (r *authSalesRepositoryStub) GetPartnerByCode(_ context.Context, code string) (*service.SalesPartner, error) {
	if code != "alice" && code != "bob" {
		return nil, service.ErrSalesNotFound
	}
	id := int64(1)
	if code == "bob" {
		id = 2
	}
	return &service.SalesPartner{ID: id, Code: code, PromotionEnabled: true}, nil
}

func (r *authSalesRepositoryStub) BindCustomer(ctx context.Context, userID int64, a *service.SalesAttribution) error {
	r.bindCalls++
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return errors.New("sales binding must use the user creation transaction")
	}
	if _, err := tx.Client().ExecContext(ctx, "INSERT INTO auth_sales_test_bindings (user_id, partner_id) VALUES (?, ?)", userID, a.PartnerID); err != nil {
		return err
	}
	if r.failBind {
		return errors.New("simulated sales persistence failure")
	}
	return nil
}

func (r *authSalesRepositoryStub) RollbackRegistration(ctx context.Context, userID int64) error {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return errors.New("sales registration rollback must be atomic")
	}
	_, err := tx.Client().ExecContext(ctx, "DELETE FROM auth_sales_test_bindings WHERE user_id = ?", userID)
	if err == nil && r.failRollback {
		return errors.New("simulated failure after removing sales ownership")
	}
	return err
}

func newAuthSalesFixture(t *testing.T) (*service.AuthService, *dbent.Client, *authSalesRepositoryStub, *service.SalesService) {
	t.Helper()
	svc, _, client := newAuthServiceForEmailBindWithRefreshCache(t, map[string]string{
		service.SettingKeyRegistrationEnabled: "true",
	}, nil, nil, newEmailBindRefreshTokenCacheStub())
	_, err := client.ExecContext(context.Background(), "CREATE TABLE auth_sales_test_bindings (user_id INTEGER PRIMARY KEY REFERENCES users(id), partner_id INTEGER NOT NULL)")
	require.NoError(t, err)
	repo := &authSalesRepositoryStub{enabled: true}
	sales := service.NewSalesService(repo, &config.Config{JWT: config.JWTConfig{Secret: "auth-sales-test-secret-with-32-characters"}})
	svc.SetSalesService(sales)
	return svc, client, repo, sales
}

func authSalesContext(t *testing.T, sales *service.SalesService, code string) context.Context {
	t.Helper()
	value, _, err := sales.IssueReferral(context.Background(), code)
	require.NoError(t, err)
	return service.ContextWithSalesReferral(context.Background(), value)
}

func TestAuthSalesIncompleteOAuthRollbackRemovesNewOwnership(t *testing.T) {
	svc, client, repo, sales := newAuthSalesFixture(t)
	ctx := authSalesContext(t, sales, "alice")
	_, user, err := svc.Register(ctx, "incomplete@example.com", "password")
	require.NoError(t, err)
	repo.enabled = false // Pausing sales must not prevent cleanup of an incomplete signup.
	require.NoError(t, svc.RollbackOAuthEmailAccountCreation(ctx, user.ID, ""))
	for _, query := range []string{
		"SELECT COUNT(*) FROM auth_sales_test_bindings WHERE user_id = ?",
		"SELECT COUNT(*) FROM users WHERE id = ? AND deleted_at IS NULL",
	} {
		rows, err := client.QueryContext(ctx, query, user.ID)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var remaining int
		require.NoError(t, rows.Scan(&remaining))
		require.NoError(t, rows.Close())
		require.Zero(t, remaining)
	}
}

func TestAuthSalesIncompleteOAuthRollbackFailurePreservesUserAndOwnership(t *testing.T) {
	svc, client, repo, sales := newAuthSalesFixture(t)
	ctx := authSalesContext(t, sales, "alice")
	_, user, err := svc.Register(ctx, "incomplete-failure@example.com", "password")
	require.NoError(t, err)
	repo.failRollback = true
	require.ErrorContains(t, svc.RollbackOAuthEmailAccountCreation(ctx, user.ID, ""), "rollback sales registration")
	for _, query := range []string{
		"SELECT COUNT(*) FROM auth_sales_test_bindings WHERE user_id = ?",
		"SELECT COUNT(*) FROM users WHERE id = ? AND deleted_at IS NULL",
	} {
		rows, err := client.QueryContext(ctx, query, user.ID)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var remaining int
		require.NoError(t, rows.Scan(&remaining))
		require.NoError(t, rows.Close())
		require.Equal(t, 1, remaining)
	}
}

func TestAuthSalesIncompleteOAuthRollbackUsesCallerTransaction(t *testing.T) {
	svc, client, _, sales := newAuthSalesFixture(t)
	ctx := authSalesContext(t, sales, "alice")
	_, user, err := svc.Register(ctx, "incomplete-outer-tx@example.com", "password")
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.RollbackOAuthEmailAccountCreation(dbent.NewTxContext(ctx, tx), user.ID, ""))
	require.NoError(t, tx.Rollback())
	active, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, active)
	rows, err := client.QueryContext(ctx, "SELECT partner_id FROM auth_sales_test_bindings WHERE user_id = ?", user.ID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next(), "the auth rollback must not commit its caller's transaction")
}

func TestAuthSalesRegistrationAtomicity(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			svc, client, repo, sales := newAuthSalesFixture(t)
			repo.failBind = fail
			_, user, err := svc.Register(authSalesContext(t, sales, "alice"), "new@example.com", "password")
			if fail {
				require.Error(t, err)
				require.Nil(t, user)
			} else {
				require.NoError(t, err)
				require.NotNil(t, user)
			}
			require.Equal(t, 1, repo.bindCalls)
			count, err := client.User.Query().Count(context.Background())
			require.NoError(t, err)
			expected := 1
			if fail {
				expected = 0
			}
			require.Equal(t, expected, count)
			rows, err := client.QueryContext(context.Background(), "SELECT COUNT(*) FROM auth_sales_test_bindings")
			require.NoError(t, err)
			defer rows.Close()
			require.True(t, rows.Next())
			var bound int
			require.NoError(t, rows.Scan(&bound))
			require.Equal(t, expected, bound)
		})
	}
}

func TestAuthSalesExistingLoginAndOAuthDoNotReassign(t *testing.T) {
	svc, _, repo, sales := newAuthSalesFixture(t)
	ctx := authSalesContext(t, sales, "alice")
	_, original, err := svc.Register(ctx, "existing@example.com", "password")
	require.NoError(t, err)
	ctx = authSalesContext(t, sales, "bob")
	_, loggedIn, err := svc.Login(ctx, "existing@example.com", "password")
	require.NoError(t, err)
	require.Equal(t, original.ID, loggedIn.ID)
	_, oauthUser, err := svc.LoginOrRegisterOAuth(ctx, "existing@example.com", "Existing")
	require.NoError(t, err)
	require.Equal(t, original.ID, oauthUser.ID)
	_, oauthUser, err = svc.LoginOrRegisterOAuthWithTokenPair(ctx, "existing@example.com", "Existing", "", "", "linuxdo")
	require.NoError(t, err)
	require.Equal(t, original.ID, oauthUser.ID)
	require.Equal(t, 1, repo.bindCalls)
}

func TestAuthSalesInvalidOrDisabledReferralDoesNotBind(t *testing.T) {
	for _, mode := range []string{"bare-code", "tampered", "expired", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			svc, _, repo, sales := newAuthSalesFixture(t)
			ctx := authSalesContext(t, sales, "alice")
			switch mode {
			case "bare-code":
				ctx = service.ContextWithSalesReferral(ctx, "alice")
			case "tampered":
				ctx = service.ContextWithSalesReferral(ctx, service.SalesReferralFromContext(ctx)+"x")
			case "expired":
				payload := base64.RawURLEncoding.EncodeToString([]byte(`{"partner_id":1,"code":"alice","expires_at":1}`))
				mac := hmac.New(sha256.New, []byte("sales-referral-v1:auth-sales-test-secret-with-32-characters"))
				_, _ = mac.Write([]byte(payload))
				ctx = service.ContextWithSalesReferral(ctx, payload+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
			case "disabled":
				repo.enabled = false
			}
			_, user, err := svc.Register(ctx, "ordinary@example.com", "password")
			require.NoError(t, err)
			require.NotNil(t, user)
			require.Zero(t, repo.bindCalls)
		})
	}
}

func TestAuthSalesOAuthCreationUsesAtomicBinding(t *testing.T) {
	for _, mode := range []string{"legacy", "provider-token-pair", "verified-email", "pending-verified-email"} {
		for _, fail := range []bool{false, true} {
			name := mode + map[bool]string{false: "-commit", true: "-rollback"}[fail]
			t.Run(name, func(t *testing.T) {
				svc, client, repo, sales := newAuthSalesFixture(t)
				repo.failBind = fail
				ctx := authSalesContext(t, sales, "alice")
				var err error
				switch mode {
				case "legacy":
					_, _, err = svc.LoginOrRegisterOAuth(ctx, "oauth@example.com", "OAuth")
				case "provider-token-pair":
					_, _, err = svc.LoginOrRegisterOAuthWithTokenPair(ctx, "oauth@example.com", "OAuth", "", "", "linuxdo")
				case "verified-email":
					_, _, err = svc.LoginOrRegisterVerifiedEmailOAuth(ctx, service.EmailOAuthIdentityInput{ProviderType: "google", ProviderSubject: "subject", Email: "oauth@example.com", EmailVerified: true})
				case "pending-verified-email":
					_, _, err = svc.RegisterVerifiedOAuthEmailAccount(ctx, "oauth@example.com", "password", "", "google")
				}
				if fail {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, repo.bindCalls)
				count, countErr := client.User.Query().Count(context.Background())
				require.NoError(t, countErr)
				expected := 1
				if fail {
					expected = 0
				}
				require.Equal(t, expected, count)
			})
		}
	}
}
