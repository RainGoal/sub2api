package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditClaudeBanMatchesLenientJSONParsing(t *testing.T) {
	const body = `{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"banned-session\"}"},"input":"before%safter"}`
	for _, tc := range []struct{ name, prefix, control string }{
		{name: "strict"},
		{name: "BOM", prefix: "\xef\xbb\xbf"},
		{name: "raw newline", control: "\n"},
		{name: "raw DEL", control: "\x7f"},
		{name: "BOM with raw NUL", prefix: "\xef\xbb\xbf", control: "\x00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "banned-session").WillReturnRows(requestAuditBanRows("banned-session"))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""))
			router.POST("/v1/messages", func(c *gin.Context) {
				_, err := httputil.ReadLenientJSONRequestBodyWithPrealloc(c.Request, 1024)
				require.ErrorIs(t, err, httputil.ErrBodyObserverRejected, "ban must stop the request before forwarding")
				require.Equal(t, "banned-session", requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
			})
			original := tc.prefix + strings.Replace(body, "%s", tc.control, 1)
			payload := []byte(original)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(payload)))
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), "Public moderation reason")
			require.Equal(t, original, string(payload))
		})
	}
}

func TestRequestAuditClaudeParsingUsesBusinessNormalizationLimit(t *testing.T) {
	const template = `{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"banned-session\"}"},"input":"%s"}`
	payload := []byte(strings.Replace(template, "%s", strings.Repeat("\x00", 128), 1))
	for _, limit := range []int64{512, 1024} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			if limit == 1024 {
				mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "banned-session").WillReturnRows(requestAuditBanRows("banned-session"))
			}
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", limit))
			router.POST("/v1/messages", func(c *gin.Context) {
				_, err := httputil.ReadLenientJSONRequestBodyWithPrealloc(c.Request, limit)
				if limit == 1024 {
					require.ErrorIs(t, err, httputil.ErrBodyObserverRejected)
					return
				}
				var tooLarge *http.MaxBytesError
				require.ErrorAs(t, err, &tooLarge)
				require.Equal(t, limit, tooLarge.Limit)
				require.Empty(t, requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
				c.Status(http.StatusRequestEntityTooLarge)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(payload)))
			if limit == 1024 {
				require.Equal(t, http.StatusForbidden, recorder.Code)
			} else {
				require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
			}
		})
	}
}
