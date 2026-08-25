package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type seedanceConnectivityAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *seedanceConnectivityAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account != nil && r.account.ID == id {
		copy := *r.account
		return &copy, nil
	}
	return nil, ErrAccountNotFound
}

type seedanceConnectivityUpstream struct {
	HTTPUpstream
	request *http.Request
}

func (u *seedanceConnectivityUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.request = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[]}`)),
	}, nil
}

func TestAccountTestServiceSeedanceUsesCostFreeModelsProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{
		ID: 25, Platform: PlatformSeedance, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                 "seedance-secret",
			"base_url":                "https://api.bblabu.ai/v1",
			"header_override_enabled": true,
			"header_overrides":        map[string]any{"X-Custom": "custom-value"},
		},
	}
	upstream := &seedanceConnectivityUpstream{}
	svc := NewAccountTestService(
		&seedanceConnectivityAccountRepo{account: account}, nil, nil, nil, nil, upstream,
		&config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}, nil,
	)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/25/test", nil)

	err := svc.TestAccountConnection(c, account.ID, "", "", AccountTestModeDefault)

	require.NoError(t, err)
	require.NotNil(t, upstream.request)
	require.Equal(t, http.MethodGet, upstream.request.Method)
	require.Equal(t, "https://api.bblabu.ai/v1/models", upstream.request.URL.String())
	require.Equal(t, "Bearer seedance-secret", upstream.request.Header.Get("Authorization"))
	require.Equal(t, []string{"custom-value"}, upstream.request.Header["x-custom"])
	require.Contains(t, recorder.Body.String(), `"success":true`)
}
