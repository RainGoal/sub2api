package service

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
)

func videoContentIsJSON(resp *http.Response) bool {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func validateVideoContentResponse(resp *http.Response) error {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable) ||
		(resp.StatusCode != http.StatusRequestedRangeNotSatisfiable && !strings.HasPrefix(mediaType, "video/") && mediaType != "application/octet-stream") {
		return fmt.Errorf("video content download returned an invalid response")
	}
	return nil
}

// Resolve the provider's optional JSON content response behind the same public
// byte endpoint. Never attach account credentials or header overrides to a CDN.
func (s *OpenAIGatewayService) resolveVideoContentURL(ctx context.Context, c *gin.Context, account *Account, parser videoprovider.ContentURLParser, source *http.Response) (*http.Response, error) {
	defer func() { _ = source.Body.Close() }()
	const limit = 1 << 20
	body, err := io.ReadAll(io.LimitReader(source.Body, limit+1))
	if err != nil || len(body) > limit {
		return nil, fmt.Errorf("video content URL response is unavailable or too large")
	}
	contentURL, err := parser.ParseContentURL(body)
	if err != nil {
		return nil, err
	}
	ctx = WithHTTPUpstreamPublicHostsOnly(WithHTTPUpstreamRedirectsDisabled(ctx))
	for redirects := 0; redirects <= 3; redirects++ {
		parsed, err := url.Parse(contentURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || urlvalidator.IsBlockedHost(parsed.Hostname()) {
			return nil, fmt.Errorf("video content URL is not a public HTTPS URL")
		}
		if _, err := s.validateOutboundURL(contentURL); err != nil {
			return nil, fmt.Errorf("video content URL is not allowed")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, contentURL, nil)
		if err != nil {
			return nil, fmt.Errorf("cannot build video download request")
		}
		req.Header.Set("Accept", "video/*,application/octet-stream")
		if c != nil {
			for _, name := range []string{"Range", "If-Range"} {
				if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
					req.Header.Set(name, value)
				}
			}
		}
		resp, err := s.httpUpstream.Do(req, accountProxyURL(account), account.ID, account.Concurrency)
		if err != nil {
			return nil, fmt.Errorf("video content download failed")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location, locationErr := resp.Location()
			_ = resp.Body.Close()
			if locationErr != nil || redirects == 3 {
				return nil, fmt.Errorf("video content redirect is invalid or exceeds the limit")
			}
			contentURL = parsed.ResolveReference(location).String()
			continue
		}
		if err := validateVideoContentResponse(resp); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		return resp, nil
	}
	return nil, fmt.Errorf("video content redirect exceeds the limit")
}
