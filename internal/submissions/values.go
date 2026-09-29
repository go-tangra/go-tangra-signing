package submissions

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// MaxValueLength bounds one text value (runes).
const MaxValueLength = 2000

// TextValued reports whether a field type takes a text value (the others
// take an uploaded image or file, or the signature itself).
func TextValued(typ string) bool {
	switch typ {
	case "text", "number", "date", "checkbox", "select", "radio", "cells":
		return true
	}
	return false
}

// ValidValue checks a text value against its field ("" is always valid:
// requiredness is checked separately). Numbers accept a decimal comma.
func ValidValue(f store.Field, v string) bool {
	if v == "" {
		return true
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > MaxValueLength || strings.IndexFunc(v, isControl) >= 0 {
		return false
	}
	switch f.Type {
	case "text":
		return true
	case "cells":
		return utf8.RuneCountInString(v) <= 200
	case "number":
		n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(v), ",", "."), 64)
		return err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
	case "date":
		_, err := time.Parse(time.DateOnly, v)
		return err == nil
	case "checkbox":
		return v == "true" || v == "false"
	case "select", "radio":
		return slices.Contains(f.Options, v)
	default:
		return false
	}
}

func isControl(r rune) bool { return unicode.IsControl(r) && r != '\t' }

// Filled reports whether a field has a value for requiredness: a text value
// (a checkbox must be checked), or an upload for image-like fields.
func Filled(f store.Field, v string, hasUpload bool) bool {
	switch f.Type {
	case "checkbox":
		return v == "true"
	case "image", "file":
		return hasUpload
	case "signature", "initials", "stamp":
		return true // drawn from the signature, the typed name or the caption
	default:
		return strings.TrimSpace(v) != ""
	}
}
