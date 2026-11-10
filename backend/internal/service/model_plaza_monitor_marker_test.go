package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaMonitorMarkerIsSeparateAndKeyBound(t *testing.T) {
	for _, tc := range []struct {
		name, header, signature string
		want                    bool
	}{
		{"missing", ModelPlazaMonitorProbeHeaderName, "", false},
		{"legacy", ChannelMonitorProbeHeaderName, BuildChannelMonitorProbeHeader("key", time.Now()), false},
		{"wrong key", ModelPlazaMonitorProbeHeaderName, BuildChannelMonitorProbeHeader("other", time.Now()), false},
		{"valid", ModelPlazaMonitorProbeHeaderName, BuildChannelMonitorProbeHeader("key", time.Now()), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set(tc.header, tc.signature)
			ApplyModelPlazaMonitorProbe(c, "key")
			require.Equal(t, tc.want, IsModelPlazaMonitorProbe(c.Request.Context()))
			require.Equal(t, tc.want, IsChannelMonitorProbe(c.Request.Context()))
		})
	}
}
