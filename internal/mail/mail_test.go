package mail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"
)

type call struct {
	tenant, key, to, corr string
	vars                  map[string]string
}

type fake struct {
	calls []call
	res   notifyclient.Result
	err   error
}

func (f *fake) SendKey(_ context.Context, tenant, key, to string, vars map[string]string, corr string) (notifyclient.Result, error) {
	f.calls = append(f.calls, call{tenant, key, to, corr, vars})
	return f.res, f.err
}

func TestLinks(t *testing.T) {
	m := Mailer{PortalBaseURL: "https://portal.example.org:8443/"}
	for got, want := range map[string]string{
		m.SignLink("s1"):      "https://portal.example.org:8443/signing/sign/s1",
		m.SubmissionLink("x"): "https://portal.example.org:8443/signing/submissions/x",
		m.InboxLink():         "https://portal.example.org:8443/signing",
		m.CertificateLink():   "https://portal.example.org:8443/signing/certificate",
	} {
		if got != want {
			t.Errorf("%s != %s", got, want)
		}
	}
}

func TestSend(t *testing.T) {
	var logs bytes.Buffer
	f := &fake{res: notifyclient.Result{Sent: true}}
	m := Mailer{Sender: f, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	vars := map[string]string{"document": "Contract", "signer": "Ivan", "link": "https://x"}
	if r := m.Send(context.Background(), "t1", Invitation, "ivan@example.org", vars, "sub-1"); r != "" {
		t.Fatalf("sent: %q", r)
	}
	if c := f.calls[0]; c.tenant != "t1" || c.key != Invitation || c.to != "ivan@example.org" || c.corr != "sub-1" || c.vars["document"] != "Contract" {
		t.Fatalf("call %+v", c)
	}
	if r := m.Send(context.Background(), "t1", Invitation, " ", vars, ""); r != "no e-mail address" {
		t.Fatalf("no address: %q", r)
	}
	f.res = notifyclient.Result{Retryable: true, Reason: "throttled"}
	if r := m.Send(context.Background(), "t1", Reminder, "a@b", vars, ""); r != "throttled" {
		t.Fatalf("retryable: %q", r)
	}
	f.res = notifyclient.Result{}
	if r := m.Send(context.Background(), "t1", Reminder, "a@b", vars, ""); r != "not delivered" {
		t.Fatalf("unconfirmed: %q", r)
	}
	f.err = errors.New("rpc error: code = FailedPrecondition desc = email not configured " + strings.Repeat("x", 300))
	r := m.Send(context.Background(), "t1", Completed, "a@b", vars, "")
	if !strings.HasPrefix(r, "rpc error") || len([]rune(r)) != maxReasonLength {
		t.Fatalf("permanent: %q", r)
	}
	if !strings.Contains(logs.String(), "signing mail not sent") || strings.Contains(logs.String(), "Ivan") {
		t.Fatalf("log must name the key only: %s", logs.String())
	}
	if r := (Mailer{}).Send(context.Background(), "t1", Completed, "a@b", nil, ""); r != unreachableMessage {
		t.Fatalf("no sender: %q", r)
	}
}
