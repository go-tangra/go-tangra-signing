package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
)

const tn = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

var bg = context.Background()

func TestEmitterPayloadsAreContentSafe(t *testing.T) {
	rec := &Recorder{}
	em := Emitter{Pub: rec}
	em.Completed(bg, tn, CompletedPayload{SubmissionID: "s1", TemplateID: "t1", FinalVersion: 2, AuditTrail: true})
	em.Cancelled(bg, tn, CancelledPayload{SubmissionID: "s1", TemplateID: "t1", ReasonCode: ReasonDeclined})
	em.Expired(bg, tn, ExpiredPayload{SubmissionID: "s1", TemplateID: "t1"})
	em.InboxChanged(bg, tn, "maria", InboxPayload{SignerID: "sg", SubmissionID: "s1", State: "invited"})
	em.InboxChanged(bg, tn, "", InboxPayload{}) // no user: dropped
	got := rec.Events()
	if len(got) != 4 {
		t.Fatalf("events = %d", len(got))
	}
	want := []string{SubmissionCompleted, SubmissionCancelled, SubmissionExpired, Inbox}
	for i, w := range want {
		if got[i].Type != w || got[i].TenantID != tn {
			t.Fatalf("event %d = %+v", i, got[i])
		}
	}
	if got[0].Users != nil || len(got[3].Users) != 1 || got[3].Users[0] != "maria" {
		t.Fatal("targets: broadcast vs signer only")
	}
	raw, _ := json.Marshal(got[0].Payload)
	if string(raw) != `{"submission_id":"s1","template_id":"t1","final_version":2,"audit_trail":true}` {
		t.Fatalf("payload = %s", raw)
	}
	if len(rec.OfType(Inbox)) != 1 || len(Types) != 4 {
		t.Fatal("types")
	}
	// The zero emitter is a no-op.
	Emitter{}.Completed(bg, tn, CompletedPayload{})
	Emitter{}.Cancelled(bg, tn, CancelledPayload{})
	Emitter{}.Expired(bg, tn, ExpiredPayload{})
	Emitter{}.InboxChanged(bg, tn, "u", InboxPayload{})
}

func TestHubPublisher(t *testing.T) {
	HubPublisher{}.Publish(bg, tn, nil, SubmissionExpired, ExpiredPayload{}) // nil hub: no-op
	mem := stream.NewMemory()
	hub := stream.NewHub(mem, stream.Config{}, nil)
	defer hub.Close()
	HubPublisher{Hub: hub}.Publish(bg, "", nil, SubmissionExpired, ExpiredPayload{}) // no tenant: dropped
	HubPublisher{Hub: hub}.Publish(bg, tn, nil, SubmissionExpired, ExpiredPayload{SubmissionID: "s"})
	HubPublisher{Hub: hub}.Publish(bg, tn, []string{"maria"}, Inbox, InboxPayload{SignerID: "sg"})
	deadline := time.Now().Add(time.Second)
	for mem.Len(stream.Key(tn)) != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if mem.Len(stream.Key(tn)) != 2 {
		t.Fatalf("events on the tenant stream = %d", mem.Len(stream.Key(tn)))
	}
}
