package store

import "github.com/go-tangra/go-tangra/v4/listquery"

// List definitions of the signing data tables (specs/032-server-side-tables
// in go-tangra, contracts/sortable-fields.md "signing"). Sort fields map to
// constant expressions only; the tie-breaker is the row's unique id. NotNull
// marks NOT NULL columns (the inbox JOIN is inner): their ORDER BY carries no
// NULLS LAST, so btree indexes serve both directions.
var (
	// TemplateList pages signing_templates.
	TemplateList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"name":       {Expr: "name", Text: true, NotNull: true},
			"status":     {Expr: "status", Text: true, NotNull: true},
			"updated_at": {Expr: "updated_at", DefaultDir: listquery.Desc, NotNull: true},
		},
		Default: "updated_at", TieBreak: "id",
	}
	// SubmissionList pages signing_submissions ("title" is the submission name).
	SubmissionList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"title":        {Expr: "name", Text: true, NotNull: true},
			"status":       {Expr: "status", Text: true, NotNull: true},
			"created_at":   {Expr: "created_at", DefaultDir: listquery.Desc, NotNull: true},
			"completed_at": {Expr: "completed_at", DefaultDir: listquery.Desc},
		},
		Default: "created_at", TieBreak: "id",
	}
	// CertificateList pages signing_certificates ("subject" is the subject CN).
	CertificateList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"subject":    {Expr: "subject_cn", Text: true, NotNull: true},
			"kind":       {Expr: "kind", Text: true, NotNull: true},
			"status":     {Expr: "status", Text: true, NotNull: true},
			"not_after":  {Expr: "not_after", DefaultDir: listquery.Desc, NotNull: true},
			"created_at": {Expr: "created_at", DefaultDir: listquery.Desc, NotNull: true},
		},
		Default: "created_at", TieBreak: "id",
	}
	// InboxList pages the caller's signer slots (signing_signers sg JOIN
	// signing_submissions s): "title" is the submission name, "status" the
	// slot's status.
	InboxList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"created_at": {Expr: "s.created_at", DefaultDir: listquery.Desc, NotNull: true},
			"title":      {Expr: "s.name", Text: true, NotNull: true},
			"status":     {Expr: "sg.status", Text: true, NotNull: true},
		},
		Default: "created_at", TieBreak: "sg.id",
	}
	// ExportOrder walks a whole table in primary-key order (backup export):
	// stable while rows are updated, independent of the user-facing defaults.
	ExportOrder = listquery.Spec{
		Fields:  map[string]listquery.Field{"id": {Expr: "id"}},
		Default: "id", TieBreak: "id",
	}
)

// ListRequest builds the request of an internal or already-validated caller:
// values the Spec rejects fall back to its defaults, so repositories never
// fail on paging input.
func ListRequest(s listquery.Spec, page, size int, sort string, order listquery.Dir) listquery.Request {
	if r, err := listquery.New(page, size, sort, order, s); err == nil {
		return r
	}
	if size > maxSize(s) {
		size = maxSize(s)
	}
	if r, err := listquery.New(page, size, "", "", s); err == nil {
		return r
	}
	r, _ := listquery.New(0, 0, "", "", s)
	return r
}

func maxSize(s listquery.Spec) int {
	if s.MaxSize > 0 {
		return s.MaxSize
	}
	return listquery.MaxPageSize
}
