// Package metrics holds the signing module's OpenTelemetry instruments. They
// are created on the framework's meter (freya App.Metrics().Meter), so the
// admin listener's /metrics renders them next to the framework instruments:
//
//	signing_signings_total{method,outcome}   signing attempts (local_certificate | qes; ok | refused | error)
//	signing_pin_failures_total               wrong PINs
//	signing_qes_total{phase,outcome}         QES prepare / complete
//	signing_mail_failures_total{template}    e-mails notification could not send
//	signing_jobs_backlog                     unfinished audit-trail jobs (observable)
//
// Labels carry closed vocabularies only — never tenant, user or submission
// ids, names or values (SR-007). Every method is nil-safe so services can run
// without metrics in tests.
package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Scope is the instrumentation scope name.
const Scope = "github.com/go-tangra/go-tangra-signing/v4"

// Outcomes (closed set).
const (
	OutcomeOK      = "ok"
	OutcomeRefused = "refused"
	OutcomeError   = "error"
)

// Backlog reports the number of unfinished jobs.
type Backlog func(ctx context.Context) (int64, error)

// Metrics are the signing instruments.
type Metrics struct {
	signings    metric.Int64Counter
	pinFailures metric.Int64Counter
	qes         metric.Int64Counter
	mail        metric.Int64Counter
}

// New creates the instruments on meter; backlog (optional) feeds the job gauge.
func New(meter metric.Meter, backlog Backlog) (*Metrics, error) {
	m := &Metrics{}
	var err error
	if m.signings, err = meter.Int64Counter("signing.signings", metric.WithDescription("Signing attempts by method and outcome")); err != nil {
		return nil, err
	}
	if m.pinFailures, err = meter.Int64Counter("signing.pin.failures", metric.WithDescription("Wrong PINs entered")); err != nil {
		return nil, err
	}
	if m.qes, err = meter.Int64Counter("signing.qes", metric.WithDescription("Qualified signature prepare/complete by outcome")); err != nil {
		return nil, err
	}
	if m.mail, err = meter.Int64Counter("signing.mail.failures", metric.WithDescription("E-mails the notification module could not send")); err != nil {
		return nil, err
	}
	if backlog != nil {
		_, err = meter.Int64ObservableUpDownCounter("signing.jobs.backlog", metric.WithDescription("Unfinished audit-trail jobs"),
			metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
				ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				n, err := backlog(ctx)
				if err != nil {
					return nil // a failed lookup reports nothing this cycle
				}
				o.Observe(n)
				return nil
			}))
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Signing records one signing attempt.
func (m *Metrics) Signing(method, outcome string) {
	if m != nil {
		m.signings.Add(context.Background(), 1, metric.WithAttributes(attribute.String("method", method), attribute.String("outcome", outcome)))
	}
}

// PINFailure counts one wrong PIN.
func (m *Metrics) PINFailure() {
	if m != nil {
		m.pinFailures.Add(context.Background(), 1)
	}
}

// QES records one prepare or complete.
func (m *Metrics) QES(phase, outcome string) {
	if m != nil {
		m.qes.Add(context.Background(), 1, metric.WithAttributes(attribute.String("phase", phase), attribute.String("outcome", outcome)))
	}
}

// MailFailure counts one failed e-mail by template key.
func (m *Metrics) MailFailure(template string) {
	if m != nil {
		m.mail.Add(context.Background(), 1, metric.WithAttributes(attribute.String("template", template)))
	}
}
