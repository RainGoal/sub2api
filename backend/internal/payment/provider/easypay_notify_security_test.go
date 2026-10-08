package provider

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func easyPayNotifyTestProvider(t *testing.T) *EasyPay {
	t.Helper()
	e, err := NewEasyPay("notify-test", map[string]string{
		"pid": "1001", "pkey": "test-only-secret", "paymentMode": paymentModePopup,
		"apiBase":   "https://pay.example.invalid",
		"notifyUrl": "https://app.example.invalid/api/v1/payment/webhook/easypay",
		"returnUrl": "https://app.example.invalid/payment/result",
	})
	require.NoError(t, err)
	return e
}

func easyPayNotifyTestParams() map[string]string {
	return map[string]string{
		"pid": "1001", "trade_no": "UPSTREAM123", "out_trade_no": "ORDER123",
		"type": "alipay", "name": "Balance recharge", "money": "25.00",
		"trade_status": tradeStatusSuccess,
	}
}

func easyPayNotifyTestBody(e *EasyPay, params map[string]string) url.Values {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	values.Set("sign", easyPaySign(params, e.config["pkey"]))
	values.Set("sign_type", signTypeMD5)
	return values
}

// Keep a legacy popup URL fixture: return URL sanitization protects new orders,
// but notification verification must also protect links issued before the fix.
func TestEasyPayNotifyRejectsReusedPopupSignature(t *testing.T) {
	t.Parallel()
	for _, foldFields := range []bool{false, true} {
		name := "order_creation_fields"
		if foldFields {
			name = "only_standard_fields"
		}
		t.Run(name, func(t *testing.T) {
			e := easyPayNotifyTestProvider(t)
			resp, err := e.CreatePayment(context.Background(), payment.CreatePaymentRequest{
				OrderID: "ORDER123", Amount: "25.00", PaymentType: "alipay", Subject: "Balance recharge",
				ReturnURL: e.config["returnUrl"] + "?order_id=123&trade_status=TRADE_SUCCESS",
			})
			require.NoError(t, err)
			payURL, err := url.Parse(resp.PayURL)
			require.NoError(t, err)
			values := payURL.Query()
			params := make(map[string]string, len(values))
			for k := range values {
				params[k] = values.Get(k)
			}
			params["return_url"] = strings.TrimSuffix(params["return_url"], "&trade_status=TRADE_SUCCESS")
			params["trade_status"] = tradeStatusSuccess
			if foldFields {
				// A key allowlist alone cannot distinguish these signed strings.
				// Legacy orders may not have a merchant snapshot to reject this pid.
				params["name"] += "&notify_url=" + params["notify_url"]
				delete(params, "notify_url")
				params["pid"] += "&return_url=" + params["return_url"]
				delete(params, "return_url")
			}
			require.True(t, easyPayVerifySign(params, e.config["pkey"], values.Get("sign")))
			callback := url.Values{}
			for k, v := range params {
				callback.Set(k, v)
			}
			n, err := e.VerifyNotification(context.Background(), callback.Encode(), nil)
			require.Error(t, err, "a checkout signature must not authenticate a notification")
			require.Nil(t, n)
		})
	}
}

func TestEasyPayNotifyRejectsInvalidParameters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"missing_pid", func(p map[string]string) { delete(p, "pid") }},
		{"empty_pid", func(p map[string]string) { p["pid"] = "" }},
		{"wrong_pid", func(p map[string]string) { p["pid"] = "other-merchant" }},
		{"order_field", func(p map[string]string) { p["return_url"] = "https://app.example.invalid" }},
		{"empty_order_field", func(p map[string]string) { p["notify_url"] = "" }},
		{"unknown_signed_field", func(p map[string]string) { p["unrecognized"] = "value" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := easyPayNotifyTestProvider(t)
			params := easyPayNotifyTestParams()
			tt.mutate(params)
			n, err := e.VerifyNotification(context.Background(), easyPayNotifyTestBody(e, params).Encode(), nil)
			require.Error(t, err)
			require.Nil(t, n)
		})
	}
}

func TestEasyPayNotifyRejectsDuplicateParameters(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"pid", "out_trade_no", "trade_no", "money", "trade_status", "sign", "sign_type", "param"} {
		t.Run(key, func(t *testing.T) {
			e := easyPayNotifyTestProvider(t)
			params := easyPayNotifyTestParams()
			params["param"] = "reference"
			values := easyPayNotifyTestBody(e, params)
			values.Add(key, "conflicting-value")
			n, err := e.VerifyNotification(context.Background(), values.Encode(), nil)
			require.Error(t, err)
			require.Nil(t, n)
		})
	}
}

func TestEasyPayNotifyAcceptsStandardCallbacks(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"alipay", "wxpay", "custom_gateway_type"} {
		t.Run(method, func(t *testing.T) {
			e := easyPayNotifyTestProvider(t)
			params := easyPayNotifyTestParams()
			params["type"] = method
			params["name"] = "充值 + A&B=套餐"
			params["param"] = "reference=a&locale=zh-CN"
			n, err := e.VerifyNotification(context.Background(), easyPayNotifyTestBody(e, params).Encode(), nil)
			require.NoError(t, err)
			require.Equal(t, payment.ProviderStatusSuccess, n.Status)
			require.Equal(t, "ORDER123", n.OrderID)
			require.Equal(t, "UPSTREAM123", n.TradeNo)
			require.Equal(t, 25.0, n.Amount)
			require.Equal(t, "1001", n.Metadata["pid"])
		})
	}
}

// easyPayPoCProvider returns a provider with a fixed test credential set.
func easyPayPoCProvider() *EasyPay {
	return &EasyPay{config: map[string]string{
		"pid":       "1000",
		"pkey":      "MERCHANT_SECRET_KEY",
		"apiBase":   "https://pay.example.com",
		"notifyUrl": "https://site.example.com/api/v1/payment/webhook/easypay",
		"returnUrl": "https://site.example.com/payment/result",
	}}
}

// TestEasyPayNotifyRejectsForgedSignReuseCallback is the exact PoC payload
// that was accepted before the fix: the order's own submit.php signature
// replayed with trade_status smuggled out of the return_url value.
func TestEasyPayNotifyRejectsForgedSignReuseCallback(t *testing.T) {
	t.Parallel()
	e := easyPayPoCProvider()

	// Order creation (popup mode): return_url carries the smuggled pair as
	// its last sorted inner query key, exactly as buildPaymentReturnURL
	// would have produced before CanonicalizeReturnURL stripped user query.
	returnURL := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success&trade_status=TRADE_SUCCESS"
	createParams := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "ORDER123",
		"notify_url":   e.config["notifyUrl"],
		"return_url":   returnURL,
		"name":         "balance recharge",
		"money":        "650.00",
	}
	sign := easyPaySign(createParams, e.config["pkey"])

	// Forged callback: return_url encoded only up to status=success, then a
	// raw &trade_status=TRADE_SUCCESS promotes it to a top-level parameter.
	prefix := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success"
	cb := url.Values{}
	cb.Set("pid", "1000")
	cb.Set("type", "alipay")
	cb.Set("out_trade_no", "ORDER123")
	cb.Set("notify_url", e.config["notifyUrl"])
	cb.Set("name", "balance recharge")
	cb.Set("money", "650.00")
	cb.Set("return_url", prefix)
	rawCallback := cb.Encode() + "&trade_status=TRADE_SUCCESS" + "&sign=" + sign + "&sign_type=MD5"

	if _, err := e.VerifyNotification(context.Background(), rawCallback, nil); err == nil {
		t.Fatal("forged sign-reuse callback must be rejected")
	}
}

// TestEasyPayNotifyRejectsOrderURLReplay covers the milder variant where the
// attacker replays the complete signed pay URL unchanged: return_url itself
// is not a legitimate notify parameter and must also be rejected.
func TestEasyPayNotifyRejectsOrderURLReplay(t *testing.T) {
	t.Parallel()
	e := easyPayPoCProvider()

	createParams := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "ORDER123",
		"notify_url":   e.config["notifyUrl"],
		"return_url":   "https://site.example.com/payment/result",
		"name":         "balance recharge",
		"money":        "650.00",
	}
	createParams["sign"] = easyPaySign(createParams, e.config["pkey"])
	createParams["sign_type"] = signTypeMD5
	q := url.Values{}
	for k, v := range createParams {
		q.Set(k, v)
	}

	if _, err := e.VerifyNotification(context.Background(), q.Encode(), nil); err == nil {
		t.Fatal("replayed order URL must be rejected")
	}
}

// TestEasyPayNotifyAcceptsGenuineCallback locks in the legitimate notify
// contract: the canonical parameter set with a valid signature succeeds.
func TestEasyPayNotifyAcceptsGenuineCallback(t *testing.T) {
	t.Parallel()
	e := easyPayPoCProvider()

	params := map[string]string{
		"pid":          "1000",
		"trade_no":     "2026100622001400000001",
		"out_trade_no": "ORDER123",
		"type":         "alipay",
		"name":         "balance recharge",
		"money":        "650.00",
		"trade_status": tradeStatusSuccess,
	}
	sign := easyPaySign(params, e.config["pkey"])
	params["sign"] = sign
	params["sign_type"] = signTypeMD5
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}

	n, err := e.VerifyNotification(context.Background(), q.Encode(), nil)
	if err != nil {
		t.Fatalf("genuine callback rejected: %v", err)
	}
	if n.Status != payment.ProviderStatusSuccess {
		t.Fatalf("status = %v, want success", n.Status)
	}
	if n.OrderID != "ORDER123" || n.TradeNo != "2026100622001400000001" || n.Amount != 650.00 {
		t.Fatalf("unexpected notification: %+v", n)
	}
}

// TestEasyPayNotifyRejectsUnknownParam ensures any parameter outside the
// canonical notify set fails closed, including empty-valued ones that the
// signer itself would skip.
func TestEasyPayNotifyRejectsUnknownParam(t *testing.T) {
	t.Parallel()
	e := easyPayPoCProvider()

	params := map[string]string{
		"pid":          "1000",
		"trade_no":     "T1",
		"out_trade_no": "ORDER123",
		"type":         "alipay",
		"name":         "balance recharge",
		"money":        "650.00",
		"trade_status": tradeStatusSuccess,
	}
	sign := easyPaySign(params, e.config["pkey"])
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("sign", sign)
	q.Set("sign_type", signTypeMD5)
	q.Set("device", "") // empty value: invisible to the signer, still rejected

	if _, err := e.VerifyNotification(context.Background(), q.Encode(), nil); err == nil {
		t.Fatal("callback with unknown param must be rejected")
	}
}
