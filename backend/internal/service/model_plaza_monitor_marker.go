package service

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

const ModelPlazaMonitorProbeHeaderName = "X-Sub2API-Model-Plaza-Probe"

type modelPlazaMonitorProbeContextKey struct{}

func WithModelPlazaMonitorProbe(ctx context.Context) context.Context {
	return context.WithValue(WithChannelMonitorProbe(ctx), modelPlazaMonitorProbeContextKey{}, true)
}

func IsModelPlazaMonitorProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(modelPlazaMonitorProbeContextKey{}).(bool)
	return value
}

// Called after normal API-key authentication. A legacy monitor marker does not
// enable model-plaza budget rules, preserving the existing channel checker.
func ApplyModelPlazaMonitorProbe(c *gin.Context, credential string) {
	if c != nil && c.Request != nil && VerifyChannelMonitorProbeHeader(c.GetHeader(ModelPlazaMonitorProbeHeaderName), credential, time.Now()) {
		c.Request = c.Request.WithContext(WithModelPlazaMonitorProbe(c.Request.Context()))
	}
}
