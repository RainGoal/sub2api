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
