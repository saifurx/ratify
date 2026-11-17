package lead

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ratify/internal/consent"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
	"ratify/internal/ratelimit"
)

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

const sessionCookie = "ratify_sid"

type Handler struct {
	svc     *Service
	pool    *db.Pool
	limiter *ratelimit.Limiter
	metrics *platform.Metrics
	cfg     platform.Config
	log     *slog.Logger
}

func NewHandler(svc *Service, pool *db.Pool, limiter *ratelimit.Limiter, m *platform.Metrics, cfg platform.Config, log *slog.Logger) *Handler {
	return &Handler{svc: svc, pool: pool, limiter: limiter, metrics: m, cfg: cfg, log: log}
}

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, codec pagination.Codec) {
	// Public, unauthenticated: the tenant comes from a form token resolved by
	// a SECURITY DEFINER function, never from the body.
	mux.HandleFunc("POST /api/v1/inbound/leads", h.inbound)

	crud.Register(mux, protect, codec, h.log, "/api/v1/leads", crud.Ops[Lead, struct{}]{
		List: h.svc.List, Get: h.svc.Get, Delete: h.svc.Delete,
	})
	mux.Handle("POST /api/v1/leads/{id}/convert", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := crud.PathID(r, "id")
		var in ConvertInput
		if err == nil && r.ContentLength != 0 && platform.DecodeJSON(w, r, &in) != nil {
			err = platform.Invalid{Msg: "invalid JSON body"}
		}
		if err == nil {
			var res ConvertResult
			if res, err = h.svc.Convert(r.Context(), id, in); err == nil {
				platform.WriteJSON(w, http.StatusCreated, res)
				return
			}
		}
		platform.WriteErr(w, h.log, err)
	})))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func str(m map[string]any, k string, max int) string {
	s, _ := m[k].(string)
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// inbound handles POST /api/v1/inbound/leads?tenant_token=...
func (h *Handler) inbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)
	received := func() { platform.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "received"}) }

	// 1. Global per-IP limit before anything else (also throttles token guessing).
	ok, err := h.limiter.Allow(ctx, ratelimit.GlobalKey("inbound", "ip", ip), h.cfg.InboundIPPerMin, time.Minute)
	if err != nil { // limiter down: fail closed
		platform.WriteError(w, http.StatusServiceUnavailable, "temporarily unavailable")
		return
	}
	if !ok {
		h.metrics.Inbound("rate_limited")
		platform.WriteError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	// 2. Tenant from the token (SECURITY DEFINER lookup).
	token := r.URL.Query().Get("tenant_token")
	tid := ""
	if token != "" && len(token) <= 128 {
		if tid, err = h.pool.ResolveTenantToken(ctx, token); err != nil {
			h.log.Error("tenant token lookup failed", "err", err.Error())
			platform.WriteError(w, http.StatusServiceUnavailable, "temporarily unavailable")
			return
		}
	}
	if tid == "" {
		h.metrics.Inbound("unknown_token")
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	// 3. Per-tenant and per-tenant-per-IP limits (keys prefixed t:{tenant_id}:).
	for _, lim := range []struct {
		n     int
		parts []string
	}{{h.cfg.InboundTenantPerMin, []string{"inbound", "tenant"}}, {h.cfg.InboundIPPerMin, []string{"inbound", "ip", ip}}} {
		ok, err := h.limiter.AllowTenant(ctx, tid, lim.n, time.Minute, lim.parts...)
		if err != nil {
			platform.WriteError(w, http.StatusServiceUnavailable, "temporarily unavailable")
			return
		}
		if !ok {
			h.metrics.Inbound("rate_limited")
			platform.WriteError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
	}

	// 4. Parse and validate (validation errors say nothing about suppression).
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil || body == nil || len(body) > 50 {
		h.metrics.Inbound("invalid")
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := consent.Normalize(str(body, "email", 254))
	if !emailRE.MatchString(email) {
		h.metrics.Inbound("invalid")
		platform.WriteError(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	consentGiven, _ := body["consent"].(bool)

	// First-party cookie for session continuity (no third-party tracking).
	sid := ""
	if c, err := r.Cookie(sessionCookie); err == nil && len(c.Value) == 32 {
		sid = c.Value
	} else {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		sid = hex.EncodeToString(b)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sid, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})

	utm := map[string]string{}
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content"} {
		if v := r.URL.Query().Get(k); v != "" {
			if len(v) > 200 {
				v = v[:200]
			}
			utm[k] = v
		}
	}
	res, err := h.svc.Ingest(ctx, tid, IngestInput{
		Email: email, FirstName: str(body, "first_name", 200), LastName: str(body, "last_name", 200),
		Company: str(body, "company", 200), Domain: str(body, "domain", 253), Source: str(body, "source", 64),
		Consent: consentGiven, Raw: body, UTM: utm, Referrer: truncate(r.Referer(), 500),
		IP: ip, UserAgent: truncate(r.UserAgent(), 500), SessionID: sid,
	})
	if err != nil {
		platform.WriteErr(w, h.log, err)
		return
	}
	// Accepted and Suppressed are indistinguishable to the sender: same
	// status, same body. Suppression is only ever counted, never logged.
	if res == Suppressed {
		h.metrics.Inbound("suppressed")
	} else {
		h.metrics.Inbound("accepted")
	}
	received()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
