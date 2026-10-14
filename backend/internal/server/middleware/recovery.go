package middleware

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Recovery converts panics into the project's standard JSON error envelope.
//
// It preserves Gin's broken-pipe handling by not attempting to write a response
// when the client connection is already gone.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			recoveredErr, _ := recovered.(error)

			// Streaming handlers use ErrAbortHandler to deliberately cut a response
			// whose declared payload can no longer be completed. Re-panic before
			// logging: Gin's stock recovery dumps request headers (including custom
			// credential headers) before invoking its callback.
			if errors.Is(recoveredErr, http.ErrAbortHandler) {
				panic(recovered)
			}

			if isBrokenPipe(recoveredErr) {
				if recoveredErr != nil {
					_ = c.Error(recoveredErr)
				}
				c.Abort()
				return
			}

			if gin.DefaultErrorWriter != nil {
				log.New(gin.DefaultErrorWriter, "", 0).Output(2, fmt.Sprintf("[Recovery] %v\n%s", recovered, debug.Stack())) //nolint:errcheck
			}
			if c.Writer.Written() {
				c.Abort()
				return
			}
			response.ErrorWithDetails(
				c,
				http.StatusInternalServerError,
				infraerrors.UnknownMessage,
				infraerrors.UnknownReason,
				nil,
			)
			c.Abort()
		}()
		c.Next()
	}
}

func isBrokenPipe(err error) bool {
	if err == nil {
		return false
	}

	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}

	var syscallErr *os.SyscallError
	if !errors.As(opErr.Err, &syscallErr) {
		return false
	}

	msg := strings.ToLower(syscallErr.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}
