package platform

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the Prometheus skeleton. Labels are bounded: route is the
// registered mux pattern (never the raw path), and no tenant, user or email
// ever appears in a label. Sprint 1.4b adds outbox lag, dead letters, the
// cross-tenant counter and backup age.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	errors   *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	inbound  *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratify_http_requests_total", Help: "HTTP requests by route and status."},
			[]string{"route", "status"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratify_http_errors_total", Help: "HTTP 5xx responses by route."},
			[]string{"route"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ratify_http_request_duration_seconds", Help: "HTTP request latency by route.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}},
			[]string{"route"}),
	}
	m.inbound = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ratify_inbound_leads_total", Help: "Public lead submissions by outcome (no tenant or contact labels)."},
		[]string{"outcome"})
	m.reg.MustRegister(m.inbound, m.requests, m.errors, m.latency,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Registry lets later sprints register their own collectors.
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern // set by ServeMux on this request once matched
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
		m.latency.WithLabelValues(route).Observe(time.Since(start).Seconds())
		if rec.status >= 500 {
			m.errors.WithLabelValues(route).Inc()
		}
	})
}

// Inbound counts a public lead submission outcome ("accepted", "suppressed",
// "rate_limited", "invalid", "unknown_token"). Suppression is only ever
// counted here: never logged, never labelled with a tenant or an address.
func (m *Metrics) Inbound(outcome string) {
	if m != nil {
		m.inbound.WithLabelValues(outcome).Inc()
	}
}
