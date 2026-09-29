package metrics

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/go-tangra/go-tangra/v4/observe"
)

func render(t *testing.T, fm *observe.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	fm.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

func TestInstrumentsRenderWithClosedLabels(t *testing.T) {
	fm, err := observe.NewMetrics()
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(fm.Meter(Scope), func(context.Context) (int64, error) { return 3, nil })
	if err != nil {
		t.Fatal(err)
	}
	m.Signing("local_certificate", OutcomeOK)
	m.Signing("qes", OutcomeRefused)
	m.PINFailure()
	m.QES("prepare", OutcomeOK)
	m.MailFailure("signing.invitation")
	body := render(t, fm)
	for _, want := range []string{
		`signing_signings_total{method="local_certificate",outcome="ok"} 1`,
		`signing_signings_total{method="qes",outcome="refused"} 1`,
		`signing_pin_failures_total 1`,
		`signing_qes_total{outcome="ok",phase="prepare"} 1`,
		`signing_mail_failures_total{template="signing.invitation"} 1`,
		`signing_jobs_backlog 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s\n%s", want, body)
		}
	}
	if strings.Contains(body, "tenant") {
		t.Fatal("tenant label in metrics")
	}
}

func TestBacklogErrorAndNil(t *testing.T) {
	fm, err := observe.NewMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(fm.Meter(Scope), func(context.Context) (int64, error) { return 0, errors.New("db down") }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(render(t, fm), "signing_jobs_backlog ") {
		t.Fatal("failed lookup observed")
	}
	var m *Metrics
	m.Signing("qes", OutcomeOK)
	m.PINFailure()
	m.QES("complete", OutcomeError)
	m.MailFailure("x")
	if _, err := New(noop.NewMeterProvider().Meter("x"), nil); err != nil {
		t.Fatal(err)
	}
}

// failing meter: every instrument constructor fails in turn.
type failMeter struct {
	noop.Meter
	failAt, n int
}

func (f *failMeter) fail() bool { f.n++; return f.n == f.failAt }

func (f *failMeter) Int64Counter(name string, o ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if f.fail() {
		return nil, errors.New("boom")
	}
	return f.Meter.Int64Counter(name, o...)
}

func (f *failMeter) Int64ObservableUpDownCounter(name string, o ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	if f.fail() {
		return nil, errors.New("boom")
	}
	return f.Meter.Int64ObservableUpDownCounter(name, o...)
}

func TestConstructorErrors(t *testing.T) {
	backlog := func(context.Context) (int64, error) { return 0, nil }
	for i := 1; i <= 5; i++ {
		if _, err := New(&failMeter{failAt: i}, backlog); err == nil {
			t.Errorf("failure %d not surfaced", i)
		}
	}
}
