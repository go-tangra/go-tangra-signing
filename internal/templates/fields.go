package templates

import (
	"errors"
	"regexp"
	"strings"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/rules"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Field types (FR-002).
var FieldTypes = map[string]bool{
	"text": true, "number": true, "signature": true, "initials": true, "date": true, "checkbox": true,
	"select": true, "radio": true, "image": true, "file": true, "cells": true, "stamp": true,
}

// Condition operators and modes (FR-042).
var (
	ruleOps = map[string]bool{"eq": true, "neq": true, "contains": true, "empty": true, "not_empty": true, "checked": true, "unchecked": true}
	modes   = map[string]bool{"all": true, "any": true}
	effects = map[string]bool{"visible": true, "required": true}
)

var (
	partyKeyRE = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	fieldIDRE  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// geometry tolerance for rounding in the browser.
const eps = 1e-6

// ValidateParties checks the party list (1..max, unique keys and names).
func ValidateParties(parties []store.Party, max int) error {
	if len(parties) == 0 || len(parties) > max {
		return apperr.InvalidField.WithField("parties")
	}
	keys, names := map[string]bool{}, map[string]bool{}
	for _, p := range parties {
		name := strings.TrimSpace(p.Name)
		if !partyKeyRE.MatchString(p.Key) || name == "" || len(name) > 80 || keys[p.Key] || names[strings.ToLower(name)] {
			return apperr.InvalidField.WithField("parties")
		}
		keys[p.Key], names[strings.ToLower(name)] = true, true
	}
	return nil
}

// ValidateFields checks every field against the parties and the page count
// (FR-002): unique ids and names, known types, existing party, page in range,
// geometry inside the page, options of select/radio, and structurally valid
// conditions and formulas. It returns the offending field's name.
func ValidateFields(fields []store.Field, parties []store.Party, pages, max int) error {
	if len(fields) > max {
		return apperr.InvalidField.WithField("fields")
	}
	partyKeys := map[string]bool{}
	for _, p := range parties {
		partyKeys[p.Key] = true
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, f := range fields {
		ids[f.ID] = true
	}
	for _, f := range fields {
		name := strings.TrimSpace(f.Name)
		bad := func() error { return apperr.InvalidField.WithField(fieldLabel(f)) }
		switch {
		case !fieldIDRE.MatchString(f.ID), name == "", len(name) > 120, names[strings.ToLower(name)],
			!FieldTypes[f.Type], !partyKeys[f.Party], f.Page < 1 || f.Page > pages,
			f.X < 0, f.Y < 0, f.W <= 0, f.H <= 0, f.X+f.W > 1+eps, f.Y+f.H > 1+eps,
			f.FontSize < 0, f.FontSize > 72, len(f.Font) > 40, len(f.Default) > 2000, len(f.Formula) > 500, len(f.Options) > 50:
			return bad()
		}
		names[strings.ToLower(name)] = true
		if f.Type == "select" || f.Type == "radio" {
			if len(f.Options) == 0 {
				return bad()
			}
			seen := map[string]bool{}
			for _, o := range f.Options {
				if strings.TrimSpace(o) == "" || len(o) > 120 || seen[o] {
					return bad()
				}
				seen[o] = true
			}
			if f.Default != "" && !seen[f.Default] {
				return bad()
			}
		} else if len(f.Options) > 0 {
			return bad()
		}
		if f.Formula != "" && f.Type != "number" {
			return bad()
		}
		if c := f.Conditions; c != nil {
			if !modes[c.Mode] || !effects[c.Effect] || len(c.Rules) == 0 || len(c.Rules) > 20 {
				return apperr.InvalidRule.WithField(fieldLabel(f))
			}
			for _, r := range c.Rules {
				if !ruleOps[r.Op] || !ids[r.Field] || r.Field == f.ID || len(r.Value) > 500 {
					return apperr.InvalidRule.WithField(fieldLabel(f))
				}
			}
		}
	}
	return nil
}

// ValidateRules refuses syntax errors, unknown fields and cycles in the
// conditions and formulas, naming the offending field (FR-042).
func ValidateRules(fields []store.Field) error {
	if err := rules.Validate(fields); err != nil {
		var re *rules.Error
		if errors.As(err, &re) {
			return apperr.InvalidRule.WithField(re.Field).WithDetail(map[string]any{"message": re.Msg})
		}
		return err
	}
	return nil
}

func fieldLabel(f store.Field) string {
	if n := strings.TrimSpace(f.Name); n != "" && len(n) <= 120 {
		return n
	}
	if len(f.ID) <= 64 {
		return f.ID
	}
	return "fields"
}

// ReadyToActivate checks that every party has at least one field (a signer
// with nothing to fill would sign blindly).
func ReadyToActivate(t store.Template) error {
	if len(t.Fields) == 0 {
		return apperr.InvalidField.WithField("fields")
	}
	has := map[string]bool{}
	for _, f := range t.Fields {
		has[f.Party] = true
	}
	for _, p := range t.Parties {
		if !has[p.Key] {
			return apperr.InvalidField.WithField(p.Name)
		}
	}
	return nil
}

// NormalizeTags trims, de-duplicates (case-insensitively) and bounds tags.
func NormalizeTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > 40 || strings.ContainsAny(t, ",\r\n") {
			return nil, apperr.Validation.WithField("tags")
		}
		if seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > 20 {
		return nil, apperr.Validation.WithField("tags")
	}
	return out, nil
}
