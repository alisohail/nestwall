package calendar

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Limits for fetching an external calendar.
const (
	feedTimeout      = 15 * time.Second
	feedDialTimeout  = 10 * time.Second
	maxFeedRedirects = 3
	maxFeedURLBytes  = 2048
	maxFeedETag      = 255
)

// Sentinel errors. They never include the link, because a link is a secret and url.Error text
// contains it.
var (
	errInvalidURL      = errors.New("calendar link is not a valid web address")
	errLinkNotAllowed  = errors.New("calendar link points to a network address that is not allowed")
	errFeedTooLarge    = errors.New("calendar is larger than the limit")
	errFeedUnavailable = errors.New("calendar could not be fetched")
)

// feedFetcher downloads external calendars. It refuses private, loopback and link-local addresses
// at connect time, so a redirect or a DNS answer cannot reach them. Loopback is only allowed in
// tests.
type feedFetcher struct {
	client        *http.Client
	allowLoopback bool
}

// feedResult is one download. NotModified means the stored ETag still matches and Body is empty.
type feedResult struct {
	Body        []byte
	ETag        string
	NotModified bool
}

func newFeedFetcher(allowLoopback bool, timeout time.Duration) *feedFetcher {
	f := &feedFetcher{allowLoopback: allowLoopback}
	dialer := &net.Dialer{
		Timeout: feedDialTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errLinkNotAllowed
			}
			ip := net.ParseIP(host)
			if ip == nil || blockedAddress(ip, allowLoopback) {
				return errLinkNotAllowed
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: feedDialTimeout,
	}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxFeedRedirects {
				return errFeedUnavailable
			}
			return f.checkURL(req.URL)
		},
	}
	return f
}

// parseFeedURL checks a link typed by the user. It returns the parsed link or a sentinel error.
func (f *feedFetcher) parseFeedURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxFeedURLBytes {
		return nil, errInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errInvalidURL
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

// checkURL allows http and https links without credentials. A literal IP address is checked here
// too, so it fails before any connection is attempted.
func (f *feedFetcher) checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return errLinkNotAllowed
	}
	if u.User != nil {
		return errLinkNotAllowed
	}
	host := u.Hostname()
	if host == "" {
		return errInvalidURL
	}
	if ip := net.ParseIP(host); ip != nil && blockedAddress(ip, f.allowLoopback) {
		return errLinkNotAllowed
	}
	return nil
}

// fetch downloads a calendar. When etag is set, the request is conditional, and a 304 response
// returns NotModified. Any failure returns one of the sentinel errors.
func (f *feedFetcher) fetch(ctx context.Context, link *url.URL, etag string) (feedResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return feedResult{}, errInvalidURL
	}
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	req.Header.Set("User-Agent", "NestWall calendar")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return feedResult{}, feedFailure(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && etag != "" {
		return feedResult{ETag: etag, NotModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return feedResult{}, errFeedUnavailable
	}
	if resp.ContentLength > maxImportBytes {
		return feedResult{}, errFeedTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImportBytes+1))
	if err != nil {
		return feedResult{}, feedFailure(err)
	}
	if len(body) > maxImportBytes {
		return feedResult{}, errFeedTooLarge
	}
	newTag := resp.Header.Get("ETag")
	if len(newTag) > maxFeedETag {
		newTag = ""
	}
	return feedResult{Body: body, ETag: newTag}, nil
}

// feedFailure maps a transport error to a sentinel, so no URL text leaves this package.
func feedFailure(err error) error {
	if errors.Is(err, errLinkNotAllowed) {
		return errLinkNotAllowed
	}
	return errFeedUnavailable
}

// blockedAddress reports whether a connection to ip must be refused. Loopback is refused unless
// allowLoopback is set, which only tests do.
func blockedAddress(ip net.IP, allowLoopback bool) bool {
	if ip.IsLoopback() {
		return !allowLoopback
	}
	if ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8, carrier-grade NAT 100.64.0.0/10 and reserved or broadcast 240.0.0.0/4.
		return v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64) || v4[0] >= 240
	}
	// NAT64 (64:ff9b::/96) and 6to4 (2002::/16) can carry a private IPv4 address inside an IPv6 one.
	if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b {
		return blockedAddress(net.IP(ip[12:16]), false)
	}
	if ip[0] == 0x20 && ip[1] == 0x02 {
		return blockedAddress(net.IP(ip[2:6]), false)
	}
	return false
}
