package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const queueLimit = 128
const batchLimit = 16
const requestTimeout = 2 * time.Second

// Options come only from the process composition root, never from a workspace.
type Options struct {
	Version    string
	Endpoint   string
	ConfigPath string
	Mode       Mode
	Getenv     func(string) string
	// AllowDevelopment only permits loopback receivers, for explicit local QA.
	AllowDevelopment bool
}

// Status describes local policy, not collector reachability or delivery.
// renart:web
// renart:web-name UsageAnalyticsStatus
type Status struct {
	Enabled        bool   `json:"enabled"`
	Active         bool   `json:"active"`
	Reason         string `json:"reason"`
	Mode           Mode   `json:"mode"`
	NoticeRequired bool   `json:"notice_required"`
	InstallationID string `json:"installation_id,omitempty"`
}

// renart:web
// renart:web-name UpdateUsageAnalyticsRequest
type UpdateRequest struct {
	Enabled           *bool `json:"enabled,omitempty"`
	AcknowledgeNotice bool  `json:"acknowledge_notice,omitempty"`
	ResetIdentity     bool  `json:"reset_identity,omitempty"`
}

type queuedEvent struct {
	event      Event
	generation string
}

type Client struct {
	mu              sync.Mutex
	sendMu          sync.Mutex
	opts            Options
	endpoint        string
	endpointValid   bool
	loopback        bool
	http            *http.Client
	queue           []queuedEvent
	cancelSend      context.CancelFunc
	cancel          context.CancelFunc
	done            chan struct{}
	wake            chan struct{}
	closed          bool
	sessionSurface  Surface
	sessionRecorded bool
}

func New(opts Options) *Client {
	if opts.ConfigPath == "" {
		opts.ConfigPath, _ = DefaultPath()
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Mode != Installation {
		opts.Mode = Unlinked
	}
	endpoint, valid, loopback := validateEndpoint(opts.Endpoint)
	ctx, cancel := context.WithCancel(context.Background())
	// Own the connection pool so shutdown cannot close idle connections used
	// by unrelated HTTP clients in the application.
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: requestTimeout, KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: requestTimeout,
	}
	c := &Client{opts: opts, endpoint: endpoint, endpointValid: valid, loopback: loopback, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), http: &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	go c.run(ctx)
	return c
}

func validateEndpoint(raw string) (string, bool, bool) {
	if raw == "" {
		return "", true, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false, false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", false, false
	}
	return u.String(), true, loopback
}

func (c *Client) policy(p preferences, err error) Status {
	status := Status{Enabled: p.Enabled == nil || *p.Enabled, Mode: c.opts.Mode, InstallationID: p.InstallationID}
	env := c.opts.Getenv
	disabled := strings.ToLower(strings.TrimSpace(env("RENART_TELEMETRY")))
	ci := strings.ToLower(strings.TrimSpace(env("CI")))
	switch {
	case disabled != "" && disabled != "on" && disabled != "true" && disabled != "1":
		status.Reason = "environment"
	case env("DO_NOT_TRACK") == "1" || env("TELEMETRY_OPTOUT") != "":
		status.Reason = "environment"
	case ci != "" && ci != "false" && ci != "0":
		status.Reason = "ci"
	case !releaseVersion.MatchString(c.opts.Version) && !(c.opts.AllowDevelopment && c.loopback):
		status.Reason = "development"
	case err != nil || c.opts.ConfigPath == "":
		status.Reason = "settings_unavailable"
	case !status.Enabled:
		status.Reason = "disabled"
	case !c.endpointValid:
		status.Reason = "invalid_endpoint"
	case c.endpoint == "":
		status.Reason = "no_collector"
	case !p.NoticeSeen:
		status.Reason = "notice_required"
		status.NoticeRequired = true
	default:
		status.Active = true
		status.Reason = "enabled"
	}
	return status
}

func (c *Client) Status() Status {
	if c == nil {
		return Status{Mode: Unlinked, Reason: "no_collector"}
	}
	p, err := readPreferences(c.opts.ConfigPath)
	return c.policy(p, err)
}

// Update changes the per-user policy atomically. Off/reset immediately discard
// this client's queued events and cancel its current request. Other processes
// recheck the file before enqueueing or sending; already delivered bytes cannot
// be recalled. Resetting an ID does not delete previously collected data.
func (c *Client) Update(req UpdateRequest) (Status, error) {
	c.mu.Lock()
	if c.cancelSend != nil {
		c.cancelSend()
	}
	c.queue = nil
	_, err := changePreferences(c.opts.ConfigPath, func(p *preferences) {
		if req.Enabled != nil {
			p.Enabled = req.Enabled
		}
		if req.AcknowledgeNotice || req.Enabled != nil {
			p.NoticeSeen = true
		}
		if req.ResetIdentity || (p.Enabled != nil && !*p.Enabled) {
			p.InstallationID = ""
		}
		p.Generation = uuid.NewString()
	})
	c.mu.Unlock()
	if err != nil {
		return c.Status(), err
	}
	// A first browser acknowledgement records the session only after policy is set.
	c.recordSession()
	return c.Status(), nil
}

func (c *Client) StartSession(surface Surface) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sessionSurface = surface
	c.mu.Unlock()
	c.recordSession()
}
func (c *Client) recordSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionRecorded || c.sessionSurface == "" {
		return
	}
	c.sessionRecorded = c.recordLocked(Observation{Name: SessionStarted, Surface: c.sessionSurface})
}

// Record is best-effort. In particular, a settings update waiting for its
// cross-process file lock must not hold up a completed query or pipeline.
func (c *Client) Record(observation Observation) {
	if c == nil || !c.mu.TryLock() {
		return
	}
	defer c.mu.Unlock()
	c.recordLocked(observation)
}

func (c *Client) recordLocked(observation Observation) bool {
	if c.closed || !observation.valid() || len(c.queue) >= queueLimit {
		return false
	}
	p, err := readPreferences(c.opts.ConfigPath)
	if !c.policy(p, err).Active {
		return false
	}
	if c.opts.Mode == Installation && p.InstallationID == "" {
		p, err = changePreferences(c.opts.ConfigPath, func(p *preferences) {
			// Recheck under the inter-process lock, including an opt-out written since read.
			if !c.policy(*p, nil).Active {
				return
			}
			if p.InstallationID == "" {
				p.InstallationID = uuid.NewString()
			}
			if p.Generation == "" {
				p.Generation = uuid.NewString()
			}
		})
		if !c.policy(p, err).Active || p.InstallationID == "" {
			return false
		}
	}
	id := ""
	if c.opts.Mode == Installation {
		id = p.InstallationID
	}
	c.queue = append(c.queue, queuedEvent{event: newEvent(observation, c.opts.Version, id, time.Now()), generation: p.Generation})
	if len(c.queue) >= batchLimit {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
	return true
}

// Sample shows the complete allowlist with synthetic values, without creating
// an ID, queuing an event, or sending anything.
func (c *Client) Sample() Event {
	id := ""
	if c.opts.Mode == Installation {
		id = "00000000-0000-4000-8000-000000000000"
	}
	e := newEvent(Observation{Name: PipelineFinished, Surface: Pipeline, Trigger: Manual, Outcome: Success, Duration: 12 * time.Second, Items: 3}, c.opts.Version, id, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	e.EventID = "00000000-0000-4000-8000-000000000001"
	return e
}

func (c *Client) run(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Flush(ctx)
		case <-c.wake:
			c.Flush(ctx)
		}
	}
}

// Flush is best-effort, with no disk queue or retry. Overload and network errors
// lose measurements rather than delaying or changing the user's work.
func (c *Client) Flush(ctx context.Context) {
	if c == nil || !c.sendMu.TryLock() {
		return
	}
	defer c.sendMu.Unlock()
	c.mu.Lock()
	p, err := readPreferences(c.opts.ConfigPath)
	if c.closed || !c.policy(p, err).Active {
		c.queue = nil
		c.mu.Unlock()
		return
	}
	events := make([]Event, 0, batchLimit)
	remaining := c.queue[:0]
	for _, queued := range c.queue {
		if queued.generation != p.Generation || (c.opts.Mode == Installation && queued.event.InstallationID != p.InstallationID) {
			continue
		}
		if len(events) < batchLimit {
			events = append(events, queued.event)
		} else {
			remaining = append(remaining, queued)
		}
	}
	c.queue = remaining
	if len(events) == 0 {
		c.mu.Unlock()
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	c.cancelSend = cancel
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); c.cancelSend = nil; c.mu.Unlock() }()
	data, err := json.Marshal(Batch{SchemaVersion: 1, Events: events})
	if err != nil || len(data) > 16<<10 {
		return
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Renart-Usage/1")
	response, err := c.http.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	c.Flush(ctx)
	cancel()
	c.mu.Lock()
	c.closed = true
	c.queue = nil
	if c.cancelSend != nil {
		c.cancelSend()
	}
	c.mu.Unlock()
	c.cancel()
	<-c.done
	c.http.CloseIdleConnections()
}
