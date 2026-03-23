package service

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	"github.com/menta2k/go-pdfplumber/pkg/plumber"
)

// DetectedField represents a placeholder region detected in the PDF.
type DetectedField struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	PageNumber    int     `json:"pageNumber"`
	XPercent      float64 `json:"xPercent"`
	YPercent      float64 `json:"yPercent"`
	WidthPercent  float64 `json:"widthPercent"`
	HeightPercent float64 `json:"heightPercent"`
	Font          string  `json:"font,omitempty"`
	FontSize      float64 `json:"fontSize,omitempty"`
}

// DetectFieldsResult is the response from the detect-fields endpoint.
type DetectFieldsResult struct {
	Fields []DetectedField `json:"fields"`
	Pages  int             `json:"pages"`
}

// placeholderRun represents a detected placeholder region.
type placeholderRun struct {
	page     int
	bbox     plumber.BBox
	font     string
	fontSize float64
}

// DetectPlaceholders analyzes a PDF and returns detected placeholder fields.
func DetectPlaceholders(pdfContent []byte) (*DetectFieldsResult, error) {
	reader := bytes.NewReader(pdfContent)
	doc, err := plumber.OpenReader(reader, int64(len(pdfContent)))
	if err != nil {
		return nil, err
	}
	defer doc.Close()

	numPages := doc.NumPages()
	result := &DetectFieldsResult{Pages: numPages}

	fieldIdx := 0
	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page, err := doc.Page(pageNum)
		if err != nil {
			continue
		}

		pageW := page.Width()
		pageH := page.Height()

		runs := findPlaceholderRuns(page, pageNum)

		for _, run := range runs {
			// Convert PDF coordinates (bottom-left origin) to percentage (top-left origin)
			xPct := (run.bbox.X0 / pageW) * 100
			yPct := ((pageH - run.bbox.Y1) / pageH) * 100
			wPct := (run.bbox.Width() / pageW) * 100
			hPct := (run.bbox.Height() / pageH) * 100

			// Adjust height to match font size + 10% padding instead of the full
			// placeholder bounding box (which includes dots/underscores extending
			// below the text baseline). This ensures the field aligns with
			// surrounding text when the signer fills it in.
			if run.fontSize > 0 {
				fontHeightPct := (run.fontSize * 1.1 / pageH) * 100
				if fontHeightPct > 1.5 { // only apply if result is reasonable
					// Adjust Y to keep the field bottom-aligned with the original bbox bottom
					yAdjust := hPct - fontHeightPct
					if yAdjust > 0 {
						yPct += yAdjust
					}
					hPct = fontHeightPct
				}
			}

			// Ensure minimum dimensions
			if hPct < 1.5 {
				hPct = 1.5
			}
			if wPct < 3.0 {
				wPct = 5.0
			}

			fieldIdx++
			result.Fields = append(result.Fields, DetectedField{
				Name:          fmt.Sprintf("Text %d", fieldIdx),
				Type:          "text",
				PageNumber:    pageNum,
				XPercent:      clamp(xPct, 0, 97),
				YPercent:      clamp(yPct, 0, 97),
				WidthPercent:  clamp(wPct, 3, 100-xPct),
				HeightPercent: clamp(hPct, 2, 100-yPct),
				Font:          run.font,
				FontSize:      run.fontSize,
			})
		}
	}

	return result, nil
}

// isDotPlaceholderChar returns true if a character is a dot or ellipsis that
// looks like a form placeholder. Uses width-based filtering: real dots are
// narrow (≤5pt) while Cyrillic letters are typically 7-14pt.
func isDotPlaceholderChar(c plumber.Char, pageW, pageH float64) bool {
	// Skip chars positioned outside page bounds (garbled CIDFont positioning)
	if c.X < 0 || c.X > pageW || c.Y < 0 || c.Y > pageH {
		return false
	}
	// Ellipsis char always counts regardless of width
	if c.Text == "\u2026" {
		return true
	}
	// Period chars with small width (≤5pt) are dot placeholders
	if c.Text == "." && c.Width > 0 && c.Width <= 5.0 {
		return true
	}
	return false
}

// findPlaceholderRuns detects placeholder regions from character runs and line objects.
func findPlaceholderRuns(page *plumber.Page, pageNum int) []placeholderRun {
	var runs []placeholderRun

	// Strategy 1: Find runs of placeholder-like characters (dots, underscores)
	charRuns := findCharacterRuns(page, pageNum)
	runs = append(runs, charRuns...)

	// Strategy 2: Find uniform-width replacement char runs.
	// Non-Unicode fonts (e.g. Windows-1251 Cyrillic) produce U+FFFD for all chars,
	// but dot/placeholder runs have uniform character width while text does not.
	uniformRuns := findUniformReplacementRuns(page, pageNum)
	runs = append(runs, uniformRuns...)

	// Strategy 3: Find long horizontal lines that could be underline placeholders
	// (exclude lines that are part of table grids)
	lineRuns := findLinePlaceholders(page, pageNum)
	runs = append(runs, lineRuns...)

	// Deduplicate overlapping runs
	runs = deduplicateRuns(runs)

	return runs
}

// findUniformReplacementRuns finds runs of U+FFFD characters where all chars
// have the same width — a strong signal for dot/underline placeholders in PDFs
// that use non-Unicode font encodings.
func findUniformReplacementRuns(page *plumber.Page, pageNum int) []placeholderRun {
	chars := page.Chars()

	const minRunLen = 10        // need enough chars to distinguish from short text
	const minRunWidth = 40.0    // minimum width in points
	const maxGap = 3.0          // max gap between consecutive chars
	const rowTolerance = 3.0    // Y tolerance for same-line grouping
	const uniformThreshold = 0.85 // 85% of chars must have the same width

	// Sort by Y descending then X ascending
	sort.Slice(chars, func(i, j int) bool {
		yi, yj := chars[i].BBox.Y0, chars[j].BBox.Y0
		if math.Abs(yi-yj) > rowTolerance {
			return yi > yj
		}
		return chars[i].BBox.X0 < chars[j].BBox.X0
	})

	var runs []placeholderRun
	var current []plumber.Char

	flushRun := func() {
		if len(current) < minRunLen {
			current = current[:0]
			return
		}
		bbox := charsBBox(current)
		if bbox.Width() < minRunWidth {
			current = current[:0]
			return
		}

		// Check width uniformity: count most common width
		widthCounts := make(map[float64]int)
		for _, c := range current {
			rounded := math.Round(c.Width*10) / 10
			widthCounts[rounded]++
		}
		maxCount := 0
		for _, cnt := range widthCounts {
			if cnt > maxCount {
				maxCount = cnt
			}
		}
		ratio := float64(maxCount) / float64(len(current))

		if ratio >= uniformThreshold {
			font, fontSize := extractFontInfo(current)
			runs = append(runs, placeholderRun{
				page:     pageNum,
				bbox:     bbox,
				font:     font,
				fontSize: fontSize,
			})
		}
		current = current[:0]
	}

	for _, c := range chars {
		if c.Text != "\ufffd" || c.Width <= 0 {
			flushRun()
			continue
		}

		if len(current) > 0 {
			last := current[len(current)-1]
			sameRow := math.Abs(c.BBox.Y0-last.BBox.Y0) < rowTolerance
			closeX := (c.BBox.X0 - last.BBox.X1) < maxGap
			if !sameRow || !closeX {
				flushRun()
			}
		}
		current = append(current, c)
	}
	flushRun()

	return runs
}

// findCharacterRuns finds consecutive runs of dot/ellipsis placeholder characters.
// Uses the same approach as go-pdfplumber's extract tool: width-based filtering,
// tight gap tolerance, and baseline coordinates for accurate spatial grouping.
func findCharacterRuns(page *plumber.Page, pageNum int) []placeholderRun {
	chars := page.Chars()
	pageW := page.Width()
	pageH := page.Height()

	const maxGap = 2.0        // max gap between consecutive dots (real dots are tightly packed)
	const yTolerance = 2.0    // Y tolerance for same-line grouping
	const minRunLen = 5       // minimum dots in a run to be a placeholder
	const minEllipsisRun = 2  // ellipsis runs need only 2 (each … = 3 dots visually)

	// Collect dot/ellipsis characters using width-based filtering
	var dots []plumber.Char
	for _, c := range chars {
		if isDotPlaceholderChar(c, pageW, pageH) {
			dots = append(dots, c)
		}
	}

	if len(dots) == 0 {
		return nil
	}

	// Sort spatially: top-to-bottom (descending Y), then left-to-right (ascending X)
	// Use baseline coordinates (c.Y, c.X) for accurate positioning
	sort.Slice(dots, func(i, j int) bool {
		if math.Abs(dots[i].Y-dots[j].Y) > yTolerance {
			return dots[i].Y > dots[j].Y
		}
		return dots[i].X < dots[j].X
	})

	// Group consecutive dots on the same line into runs
	var runs []placeholderRun
	var current []plumber.Char

	flushRun := func() {
		if len(current) == 0 {
			return
		}
		// Check if run is valid: enough dots, or has ellipsis chars
		hasEllipsis := false
		for _, c := range current {
			if c.Text == "\u2026" {
				hasEllipsis = true
				break
			}
		}
		minLen := minRunLen
		if hasEllipsis {
			minLen = minEllipsisRun
		}
		if len(current) < minLen {
			current = current[:0]
			return
		}
		bbox := charsBBox(current)
		font, fontSize := extractFontInfo(current)
		runs = append(runs, placeholderRun{
			page:     pageNum,
			bbox:     bbox,
			font:     font,
			fontSize: fontSize,
		})
		current = current[:0]
	}

	for i, d := range dots {
		if i == 0 {
			current = append(current, d)
			continue
		}

		prev := current[len(current)-1]
		sameLine := math.Abs(d.Y-prev.Y) <= yTolerance
		gap := d.X - (prev.X + prev.Width)

		if sameLine && gap < maxGap {
			current = append(current, d)
		} else {
			flushRun()
			current = append(current, d)
		}
	}
	flushRun()

	return runs
}

// findLinePlaceholders detects horizontal lines that look like underline placeholders.
func findLinePlaceholders(page *plumber.Page, pageNum int) []placeholderRun {
	lines := page.Lines()

	const minLineLen = 50.0  // minimum length in points
	const maxLineLen = 400.0 // max length (full-width lines are usually borders)

	// Collect table-grid Y positions to exclude
	tableYs := make(map[float64]int)
	for _, l := range lines {
		if l.Orientation == "horizontal" {
			tableYs[math.Round(l.Y0)]++
		}
	}

	// Find isolated horizontal lines (not part of a table grid)
	var runs []placeholderRun
	for _, l := range lines {
		if l.Orientation != "horizontal" {
			continue
		}
		lineLen := l.BBox.Width()
		if lineLen < minLineLen || lineLen > maxLineLen {
			continue
		}
		// Skip lines at Y positions with many parallel lines (table grid)
		if tableYs[math.Round(l.Y0)] > 2 {
			continue
		}

		// Get the font info from nearby text characters
		font, fontSize := findNearbyFont(page, l.BBox)

		runs = append(runs, placeholderRun{
			page:     pageNum,
			bbox:     l.BBox,
			font:     font,
			fontSize: fontSize,
		})
	}

	return runs
}

// findNearbyFont looks for text characters near a bounding box to extract font info.
func findNearbyFont(page *plumber.Page, bbox plumber.BBox) (string, float64) {
	chars := page.Chars()
	for _, c := range chars {
		if c.FontName == "" || c.Text == "\ufffd" {
			continue
		}
		// Check if char is on the same line (similar Y) and nearby
		if math.Abs(c.BBox.Y0-bbox.Y0) < 5.0 && math.Abs(c.BBox.X0-bbox.X0) < 100.0 {
			return c.FontName, c.FontSize
		}
	}
	return "", 0
}

func charsBBox(chars []plumber.Char) plumber.BBox {
	if len(chars) == 0 {
		return plumber.BBox{}
	}
	bbox := chars[0].BBox
	for _, c := range chars[1:] {
		if c.BBox.X0 < bbox.X0 {
			bbox.X0 = c.BBox.X0
		}
		if c.BBox.Y0 < bbox.Y0 {
			bbox.Y0 = c.BBox.Y0
		}
		if c.BBox.X1 > bbox.X1 {
			bbox.X1 = c.BBox.X1
		}
		if c.BBox.Y1 > bbox.Y1 {
			bbox.Y1 = c.BBox.Y1
		}
	}
	return bbox
}

func extractFontInfo(chars []plumber.Char) (string, float64) {
	for _, c := range chars {
		if c.FontName != "" {
			return c.FontName, c.FontSize
		}
	}
	return "", 0
}

// deduplicateRuns removes overlapping placeholder runs, keeping the larger one.
func deduplicateRuns(runs []placeholderRun) []placeholderRun {
	if len(runs) <= 1 {
		return runs
	}

	var result []placeholderRun
	used := make([]bool, len(runs))

	for i := range runs {
		if used[i] {
			continue
		}
		best := i
		for j := i + 1; j < len(runs); j++ {
			if used[j] {
				continue
			}
			if runsOverlap(runs[best], runs[j]) {
				if runs[j].bbox.Width() > runs[best].bbox.Width() {
					used[best] = true
					best = j
				} else {
					used[j] = true
				}
			}
		}
		result = append(result, runs[best])
		used[best] = true
	}

	return result
}

func runsOverlap(a, b placeholderRun) bool {
	if a.page != b.page {
		return false
	}
	// Check if bounding boxes overlap significantly
	xOverlap := a.bbox.X0 < b.bbox.X1 && b.bbox.X0 < a.bbox.X1
	yOverlap := math.Abs(a.bbox.Y0-b.bbox.Y0) < 5.0
	return xOverlap && yOverlap
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
 
