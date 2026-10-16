//go:build unit

package handler

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

const easyPayWebhookTestKey = "easypay-webhook-test-key"

func TestEasyPayWebhookAcceptsSignedCompletedOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			client, handler, order, queryCalls := newEasyPayWebhookSecurityFixture(t, service.OrderStatusCompleted)
			params := easyPayWebhookTestParams(order.OutTradeNo)
			// EasyPay's optional passthrough field can contain URL delimiters.
			params.Set("param", "state=paid&source=checkout")
			signEasyPayWebhookTestParams(params)

			for attempt := range 2 {
				recorder := serveEasyPayWebhookTest(handler, method, params.Encode())
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, "success", recorder.Body.String())
				require.EqualValues(t, attempt+1, queryCalls.Load(), "each verified success must query the upstream order")
			}
			current, err := client.PaymentOrder.Get(context.Background(), order.ID)
			require.NoError(t, err)
			require.Equal(t, service.OrderStatusCompleted, current.Status)
			require.Equal(t, "gateway-trade-123", current.PaymentTradeNo)
			assertEasyPayWebhookNoAccountingChanges(t, client, order.UserID)
		})
	}
}

func TestEasyPayWebhookRejectsAmbiguousSignedPayloadBeforePaymentMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name         string
		change       func(url.Values)
		duplicateKey string
	}{
		{name: "checkout return URL", change: func(v url.Values) { v.Set("return_url", "https://merchant.invalid/result") }},
		{name: "checkout notify URL", change: func(v url.Values) { v.Set("notify_url", "https://merchant.invalid/webhook") }},
		{name: "unknown field", change: func(v url.Values) { v.Set("unrecognized", "value") }},
		{name: "missing merchant", change: func(v url.Values) { v.Del("pid") }},
		{name: "wrong merchant", change: func(v url.Values) { v.Set("pid", "9999") }},
		{name: "return URL folded into merchant", change: func(v url.Values) {
			v.Set("pid", "1001&return_url=https://merchant.invalid/result")
		}},
		{name: "duplicate merchant", duplicateKey: "pid"},
		{name: "duplicate amount", duplicateKey: "money"},
		{name: "duplicate order", duplicateKey: "out_trade_no"},
		{name: "duplicate signature", duplicateKey: "sign"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				client, handler, order, queryCalls := newEasyPayWebhookSecurityFixture(t, service.OrderStatusPending)
				// The legacy order deliberately has no provider snapshot. Rejection
				// must come from the real provider before accounting can begin.
				require.Empty(t, order.ProviderSnapshot)
				mutationAttempts := 0
				client.PaymentOrder.Use(func(dbent.Mutator) dbent.Mutator {
					return dbent.MutateFunc(func(context.Context, dbent.Mutation) (dbent.Value, error) {
						mutationAttempts++
						return nil, errors.New("unexpected payment mutation")
					})
				})
				params := easyPayWebhookTestParams(order.OutTradeNo)
				if tc.change != nil {
					tc.change(params)
				}
				signEasyPayWebhookTestParams(params)
				if tc.duplicateKey != "" {
					params.Add(tc.duplicateKey, params.Get(tc.duplicateKey))
				}

				recorder := serveEasyPayWebhookTest(handler, method, params.Encode())
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Equal(t, "verify failed", recorder.Body.String())
				require.Zero(t, queryCalls.Load(), "rejected signatures must not trigger upstream confirmation")
				require.Zero(t, mutationAttempts)
				current, err := client.PaymentOrder.Get(context.Background(), order.ID)
				require.NoError(t, err)
				require.Equal(t, service.OrderStatusPending, current.Status)
				require.Empty(t, current.PaymentTradeNo)
				require.Nil(t, current.PaidAt)
				assertEasyPayWebhookNoAccountingChanges(t, client, order.UserID)
			})
		}
	}
}

func newEasyPayWebhookSecurityFixture(t *testing.T, status string) (*dbent.Client, *PaymentWebhookHandler, *dbent.PaymentOrder, *atomic.Int32) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:easypay_webhook_security?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("webhook@example.invalid").
		SetPasswordHash("hash").SetUsername("webhook").SetBalance(7).Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().SetUserID(user.ID).
		SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(10).SetPayAmount(10).SetFeeRate(0).
		SetRechargeCode("WEBHOOK-SECURITY").SetOutTradeNo("legacy-webhook-order").
		SetPaymentType(payment.TypeAlipay).SetProviderKey(payment.TypeEasyPay).
		SetPaymentTradeNo("").SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").SetSrcHost("merchant.invalid").Save(ctx)
	require.NoError(t, err)
	if status == service.OrderStatusCompleted {
		order, err = order.Update().SetPaymentTradeNo("gateway-trade-123").SetPaidAt(time.Now()).Save(ctx)
		require.NoError(t, err)
	}
	var queryCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api.php" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected query request", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse upstream query form: %v", err)
			http.Error(w, "invalid query request", http.StatusBadRequest)
			return
		}
		for field, expected := range map[string]string{
			"act": "order", "pid": "1001", "key": easyPayWebhookTestKey, "out_trade_no": order.OutTradeNo,
		} {
			if r.URL.Query().Get(field) != expected || r.PostForm.Get(field) != expected {
				t.Errorf("unexpected upstream query field: %s", field)
				http.Error(w, "incorrect order query", http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 1, "pid": "1001", "out_trade_no": order.OutTradeNo,
			"trade_no": "gateway-trade-123", "money": "10.00", "status": 1,
		}); err != nil {
			t.Errorf("write upstream query response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	easyPay, err := provider.NewEasyPay("webhook-test", map[string]string{
		"pid": "1001", "pkey": easyPayWebhookTestKey,
		"apiBase": upstream.URL, "notifyUrl": "https://merchant.invalid/webhook",
		"returnUrl": "https://merchant.invalid/result",
	})
	require.NoError(t, err)
	registry := payment.NewRegistry()
	registry.Register(easyPay)
	paymentSvc := service.NewPaymentService(client, registry, nil, nil, nil, nil, nil, nil, nil)
	return client, NewPaymentWebhookHandler(paymentSvc, registry), order, &queryCalls
}

func easyPayWebhookTestParams(orderID string) url.Values {
	return url.Values{
		"pid": {"1001"}, "type": {payment.TypeAlipay}, "out_trade_no": {orderID},
		"trade_no": {"gateway-trade-123"}, "name": {"Webhook order"},
		"money": {"10.00"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"MD5"},
	}
}

func signEasyPayWebhookTestParams(params url.Values) {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "sign" && key != "sign_type" && params.Get(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}
	digest := md5.Sum([]byte(strings.Join(parts, "&") + easyPayWebhookTestKey))
	params.Set("sign", hex.EncodeToString(digest[:]))
}

func serveEasyPayWebhookTest(handler *PaymentWebhookHandler, method, payload string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/api/v1/payment/webhook/easypay"
	if method == http.MethodGet {
		ctx.Request = httptest.NewRequest(method, path+"?"+payload, nil)
	} else {
		ctx.Request = httptest.NewRequest(method, path, strings.NewReader(payload))
		ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	handler.EasyPayNotify(ctx)
	return recorder
}

func assertEasyPayWebhookNoAccountingChanges(t *testing.T, client *dbent.Client, userID int64) {
	t.Helper()
	ctx := context.Background()
	user, err := client.User.Get(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 7.0, user.Balance)
	redeemCount, err := client.RedeemCode.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, redeemCount)
	auditCount, err := client.PaymentAuditLog.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, auditCount)
}
