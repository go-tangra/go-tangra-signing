package repotest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// listNames repeat case-insensitively equal values so the tie-breaker is
// exercised; letters and digits only, so database collation and Go agree.
var listNames = []string{"delta", "Alpha", "charlie", "Bravo", "echo", "alpha", "foxtrot", "Delta", "golf", "bravo", "hotel"}

// Lists seeds both tenants and checks every list (templates, submissions,
// certificates, inbox) for every sort field in both directions: each page
// walk returns every matching record exactly once in the Spec's order (text
// case-insensitive, nulls last, id tie-breaker), totals are per tenant, a page
// beyond the end answers the last page, and the export walk follows the
// primary key even while rows are updated between pages.
func Lists(t *testing.T, s repo.Store) {
	base := time.Now().UTC().Truncate(time.Second)
	var tpls []store.Template
	var subs []store.Submission
	var inbox []repo.InboxItem
	for i, n := range listNames {
		tp := Template(TenantA, n)
		tp.Name = fmt.Sprintf("%s%02d", n, i) // template names are unique per folder (case-insensitive)
		if i%4 == 0 {
			tp.Status = store.TemplateDraft
		}
		tp.UpdatedAt = base.Add(time.Duration(i%3) * time.Minute) // equal timestamps
		must(t, s.CreateTemplate(ctx, tp))
		tpls = append(tpls, tp)

		sub, sg, v0 := Submission(tp)
		sub.Name = n // submission titles repeat case-insensitively: the tie-breaker decides
		sub.CreatedAt = base.Add(time.Duration(i%4) * time.Minute)
		switch i % 3 {
		case 0:
			sub.Status = store.SubmissionCompleted
			c := base.Add(time.Duration(i%2) * time.Hour)
			sub.CompletedAt = &c
			sg[0].Status = store.SignerSigned
			sg[0].SignedAt = &c
		case 1:
			sub.Status = store.SubmissionDraft
			sg[0].Status = store.SignerPending
		}
		must(t, s.CreateSubmission(ctx, sub, sg, v0))
		subs = append(subs, sub)
		inbox = append(inbox, repo.InboxItem{Signer: sg[0], Submission: sub})
	}
	// Tenant B: same shapes, must never be counted for A.
	for _, n := range listNames[:3] {
		tp := Template(TenantB, n)
		must(t, s.CreateTemplate(ctx, tp))
		sub, sg, v0 := Submission(tp)
		must(t, s.CreateSubmission(ctx, sub, sg, v0))
	}
	ca := Certificate(TenantA, store.KindCA, nil, nil)
	ca.SubjectCN = "rootca"
	must(t, s.CreateCertificate(ctx, ca))
	certs := []store.Certificate{ca}
	statuses := []string{store.CertActive, store.CertRevoked, store.CertExpired}
	for i, n := range listNames {
		kind, owner := store.KindAdmin, (*string)(nil)
		if i%3 == 0 {
			kind, owner = store.KindSigner, ptr(fmt.Sprintf("owner%02d", i))
		}
		c := Certificate(TenantA, kind, &ca.ID, owner)
		c.SubjectCN = n
		c.Status = statuses[i%3]
		c.NotAfter = base.AddDate(1, 0, i%2)
		c.CreatedAt = base.Add(time.Duration(i%2) * time.Minute)
		must(t, s.CreateCertificate(ctx, c))
		certs = append(certs, c)
	}
	must(t, s.CreateCertificate(ctx, Certificate(TenantB, store.KindCA, nil, nil)))

	t.Run("templates", func(t *testing.T) {
		for field := range store.TemplateList.Fields {
			for _, dir := range []listquery.Dir{listquery.Asc, listquery.Desc} {
				want := ordered(tpls, field, dir, func(x store.Template, f string) any {
					return map[string]any{"name": x.Name, "status": x.Status, "updated_at": x.UpdatedAt}[f]
				}, func(x store.Template) string { return x.ID })
				walk(t, field, dir, want, func(p, size int) ([]string, int) {
					l, total, err := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{Page: p, PageSize: size, Sort: field, Order: dir})
					must(t, err)
					return idsOf(l, func(x store.Template) string { return x.ID }), total
				})
			}
		}
	})
	t.Run("submissions", func(t *testing.T) {
		for field := range store.SubmissionList.Fields {
			for _, dir := range []listquery.Dir{listquery.Asc, listquery.Desc} {
				want := ordered(subs, field, dir, func(x store.Submission, f string) any {
					if f == "completed_at" {
						if x.CompletedAt == nil {
							return nil
						}
						return *x.CompletedAt
					}
					return map[string]any{"title": x.Name, "status": x.Status, "created_at": x.CreatedAt}[f]
				}, func(x store.Submission) string { return x.ID })
				walk(t, field, dir, want, func(p, size int) ([]string, int) {
					l, total, err := s.ListSubmissions(ctx, TenantA, repo.SubmissionFilter{Page: p, PageSize: size, Sort: field, Order: dir})
					must(t, err)
					return idsOf(l, func(x store.Submission) string { return x.ID }), total
				})
			}
		}
	})
	t.Run("certificates", func(t *testing.T) {
		for field := range store.CertificateList.Fields {
			for _, dir := range []listquery.Dir{listquery.Asc, listquery.Desc} {
				want := ordered(certs, field, dir, func(x store.Certificate, f string) any {
					return map[string]any{"subject": x.SubjectCN, "kind": x.Kind, "status": x.Status, "not_after": x.NotAfter, "created_at": x.CreatedAt}[f]
				}, func(x store.Certificate) string { return x.ID })
				walk(t, field, dir, want, func(p, size int) ([]string, int) {
					l, total, err := s.ListCertificates(ctx, TenantA, repo.CertificateFilter{Page: p, PageSize: size, Sort: field, Order: dir})
					must(t, err)
					return idsOf(l, func(x store.Certificate) string { return x.ID }), total
				})
			}
		}
	})
	t.Run("inbox", func(t *testing.T) {
		for _, signed := range []bool{false, true} {
			var mine []repo.InboxItem
			for _, it := range inbox {
				if signed && it.Signer.Status == store.SignerSigned ||
					!signed && it.Signer.Status == store.SignerInvited && it.Submission.Status == store.SubmissionInProgress {
					mine = append(mine, it)
				}
			}
			if len(mine) < 3 {
				t.Fatalf("inbox seed too small: %d", len(mine))
			}
			for field := range store.InboxList.Fields {
				for _, dir := range []listquery.Dir{listquery.Asc, listquery.Desc} {
					want := ordered(mine, field, dir, func(x repo.InboxItem, f string) any {
						return map[string]any{"title": x.Submission.Name, "status": x.Signer.Status, "created_at": x.Submission.CreatedAt}[f]
					}, func(x repo.InboxItem) string { return x.Signer.ID })
					walk(t, fmt.Sprintf("%s signed=%v", field, signed), dir, want, func(p, size int) ([]string, int) {
						l, total, err := s.Inbox(ctx, TenantA, "maria", repo.InboxFilter{Signed: signed, Page: p, PageSize: size, Sort: field, Order: dir})
						must(t, err)
						return idsOf(l, func(x repo.InboxItem) string { return x.Signer.ID }), total
					})
				}
			}
		}
		if _, n, _ := s.Inbox(ctx, TenantB, "maria", repo.InboxFilter{}); n != 3 {
			t.Fatalf("tenant B inbox total %d", n)
		}
	})
	t.Run("totals clamp and defaults", func(t *testing.T) {
		if _, n, _ := s.ListTemplates(ctx, TenantB, repo.TemplateFilter{}); n != 3 {
			t.Fatalf("tenant B templates %d", n)
		}
		if _, n, _ := s.ListSubmissions(ctx, TenantB, repo.SubmissionFilter{}); n != 3 {
			t.Fatalf("tenant B submissions %d", n)
		}
		if _, n, _ := s.ListCertificates(ctx, TenantB, repo.CertificateFilter{}); n != 1 {
			t.Fatalf("tenant B certificates %d", n)
		}
		// Beyond the last page: the last page.
		l, total, err := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{Page: 99, PageSize: 5, Sort: "name"})
		must(t, err)
		if total != len(tpls) || len(l) != len(tpls)%5 {
			t.Fatalf("clamped page: %d of %d", len(l), total)
		}
		// Invalid internal input falls back to the defaults (updated_at desc, 25).
		l, _, err = s.ListTemplates(ctx, TenantA, repo.TemplateFilter{Page: -3, PageSize: 1000, Sort: "pdf_key", Order: "sideways"})
		must(t, err)
		want := ordered(tpls, "updated_at", listquery.Desc, func(x store.Template, _ string) any { return x.UpdatedAt },
			func(x store.Template) string { return x.ID })
		if !slices.Equal(idsOf(l, func(x store.Template) string { return x.ID }), want) {
			t.Fatal("fallback order")
		}
		// No match: page 1, nothing.
		if l, n, _ := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{Query: "zzz", Page: 4}); n != 0 || len(l) != 0 {
			t.Fatalf("empty list %d", n)
		}
	})
	t.Run("export walk", func(t *testing.T) {
		var got []string
		for p := 1; ; p++ {
			l, total, err := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{Page: p, PageSize: 4, Sort: "name", Order: listquery.Desc, Export: true})
			must(t, err)
			for _, x := range l {
				got = append(got, x.ID)
				// Touching a row mid-walk must not move it (updated_at is the default sort).
				x.UpdatedAt = time.Now().UTC().Add(time.Hour)
				_, err := s.UpdateTemplate(ctx, x, x.Version)
				must(t, err)
			}
			if len(got) >= total {
				break
			}
		}
		want := idsOf(tpls, func(x store.Template) string { return x.ID })
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("export walk not in id order / not exactly once")
		}
		var subIDs []string
		for p := 1; ; p++ {
			l, total, err := s.ListSubmissions(ctx, TenantA, repo.SubmissionFilter{Page: p, PageSize: 4, Export: true})
			must(t, err)
			subIDs = append(subIDs, idsOf(l, func(x store.Submission) string { return x.ID })...)
			if len(subIDs) >= total {
				break
			}
		}
		if !slices.IsSorted(subIDs) || len(subIDs) != len(subs) {
			t.Fatal("submission export walk")
		}
		var certIDs []string
		for p := 1; ; p++ {
			l, total, err := s.ListCertificates(ctx, TenantA, repo.CertificateFilter{Page: p, PageSize: 4, Export: true})
			must(t, err)
			certIDs = append(certIDs, idsOf(l, func(x store.Certificate) string { return x.ID })...)
			if len(certIDs) >= total {
				break
			}
		}
		if !slices.IsSorted(certIDs) || len(certIDs) != len(certs) {
			t.Fatal("certificate export walk")
		}
	})
}

// ordered is the expected id order of items for field and dir.
func ordered[T any](items []T, field string, dir listquery.Dir, key func(T, string) any, id func(T) string) []string {
	cp := slices.Clone(items)
	listquery.SortSlice(cp, listquery.Request{Sort: field, Order: dir}, key, id)
	return idsOf(cp, id)
}

func idsOf[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, x := range items {
		out = append(out, id(x))
	}
	return out
}

// walk pages through a list with a small page size and requires every record
// exactly once, in want's order, with a stable total.
func walk(t *testing.T, field string, dir listquery.Dir, want []string, fetch func(p, size int) ([]string, int)) {
	t.Helper()
	const size = 3
	var got []string
	for p := 1; p <= (len(want)+size-1)/size; p++ {
		ids, total := fetch(p, size)
		if total != len(want) {
			t.Fatalf("%s %s: total %d, want %d", field, dir, total, len(want))
		}
		got = append(got, ids...)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s %s: page walk order\n got %s\nwant %s", field, dir, strings.Join(got, ","), strings.Join(want, ","))
	}
}
