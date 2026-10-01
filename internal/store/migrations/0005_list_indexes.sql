-- +goose Up
-- Server-side sorting (go-tangra feature 032): tenant-scoped indexes for the
-- case-insensitive name/title sorts of the templates and submissions tables.
CREATE INDEX IF NOT EXISTS signing_templates_lower_name ON signing_templates (tenant_id, lower(name), id);
CREATE INDEX IF NOT EXISTS signing_submissions_lower_name ON signing_submissions (tenant_id, lower(name), id);

-- +goose Down
DROP INDEX IF EXISTS signing_submissions_lower_name;
DROP INDEX IF EXISTS signing_templates_lower_name;
