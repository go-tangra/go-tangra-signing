-- +goose Up
-- Per-tenant row-level security on every signing table. signing_app is
-- NOBYPASSRLS (created by the stack's init-db, not here); every statement runs
-- with app.tenant_id set to the caller's tenant. The scheduled task types, the
-- audit-trail job worker, the audit writer and platform-admin backup (only
-- after the caller has been authorised) set app.system='on' (with
-- app.tenant_id pinned to the nil uuid so the cast stays valid).
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['signing_template_folders','signing_templates','signing_submissions','signing_signers',
    'signing_document_versions','signing_certificates','signing_qes_preparations','signing_events','signing_jobs',
    'signing_audit_events']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on') WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')$p$, t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO signing_app', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['signing_template_folders','signing_templates','signing_submissions','signing_signers',
    'signing_document_versions','signing_certificates','signing_qes_preparations','signing_events','signing_jobs',
    'signing_audit_events']
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
  END LOOP;
END $$;
-- +goose StatementEnd
