package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"time"

	"github.com/digitorus/pdfsign/sign"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/font"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/cmd/server/assets"
	"github.com/go-tangra/go-tangra-signing/internal/data"
)

const defaultFontSize = 10.0

// PDFGenerator handles creation of signed PDF documents with field overlays.
type PDFGenerator struct {
	log     *log.Helper
	storage *data.StorageClient
}

// NewPDFGenerator creates a new PDFGenerator instance.
func NewPDFGenerator(ctx *bootstrap.Context, storage *data.StorageClient) *PDFGenerator {
	l := ctx.NewLoggerHelper("signing/service/pdf-generator")

	// Initialize pdfcpu config and install DejaVuSans for Unicode/Cyrillic support
	api.LoadConfiguration()
	if font.UserFontDir != "" {
		if err := font.InstallFontFromBytes(font.UserFontDir, "DejaVuSans", assets.DejaVuSansFont); err != nil {
			l.Warnf("failed to install DejaVuSans font: %v", err)
		} else if err := font.LoadUserFonts(); err != nil {
			l.Warnf("failed to load user fonts: %v", err)
		} else {
			l.Infof("installed DejaVuSans font into %s", font.UserFontDir)
		}
	}

	return &PDFGenerator{
		log:     l,
		storage: storage,
	}
}

// GenerateSignedPDF creates a signed PDF by overlaying field values onto the original template PDF.
func (g *PDFGenerator) GenerateSignedPDF(ctx context.Context, tenantID uint32, submissionID, templateFileKey string, fieldValues []map[string]interface{}) (string, error) {
	pdfContent, err := g.storage.Download(ctx, templateFileKey)
	if err != nil {
		return "", fmt.Errorf("failed to download template PDF: %w", err)
	}

	signedPDF, err := g.overlayFields(pdfContent, fieldValues)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed PDF: %w", err)
	}

	signedKey := fmt.Sprintf("%d/signed/%s/signed.pdf", tenantID, submissionID)
	if _, uploadErr := g.storage.UploadRaw(ctx, signedKey, signedPDF, "application/pdf"); uploadErr != nil {
		return "", fmt.Errorf("failed to upload signed PDF: %w", uploadErr)
	}

	g.log.Infof("generated signed PDF for submission %s at %s", submissionID, signedKey)
	return signedKey, nil
}

// GenerateIntermediatePDF creates a PDF with the completed signer's data overlaid.
// It uses currentPdfKey if available (previous signer's output), otherwise the original template.
func (g *PDFGenerator) GenerateIntermediatePDF(ctx context.Context, tenantID uint32, submissionID, templateFileKey, currentPdfKey string, fieldValues []map[string]interface{}) (string, error) {
	sourceKey := templateFileKey
	if currentPdfKey != "" {
		sourceKey = currentPdfKey
	}

	pdfContent, err := g.storage.Download(ctx, sourceKey)
	if err != nil {
		return "", fmt.Errorf("failed to download source PDF: %w", err)
	}

	overlaidPDF, err := g.overlayFields(pdfContent, fieldValues)
	if err != nil {
		return "", fmt.Errorf("failed to generate intermediate PDF: %w", err)
	}

	intermediateKey := fmt.Sprintf("%d/signing-progress/%s/current.pdf", tenantID, submissionID)
	if _, uploadErr := g.storage.UploadRaw(ctx, intermediateKey, overlaidPDF, "application/pdf"); uploadErr != nil {
		return "", fmt.Errorf("failed to upload intermediate PDF: %w", uploadErr)
	}

	g.log.Infof("generated intermediate PDF for submission %s at %s", submissionID, intermediateKey)
	return intermediateKey, nil
}

// overlayFields stamps text and image field values onto the PDF using pdfcpu watermarks.
func (g *PDFGenerator) overlayFields(pdfContent []byte, fieldValues []map[string]interface{}) ([]byte, error) {
	if len(fieldValues) == 0 {
		return pdfContent, nil
	}

	// Read PDF to get page dimensions for coordinate conversion
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	pdfCtx, err := api.ReadContext(bytes.NewReader(pdfContent), conf)
	if err != nil {
		return nil, fmt.Errorf("read pdf context: %w", err)
	}
	if err := api.OptimizeContext(pdfCtx); err != nil {
		return nil, fmt.Errorf("optimize pdf: %w", err)
	}
	if err := pdfCtx.XRefTable.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("ensure page count: %w", err)
	}

	// Cache page dimensions
	type pageDims struct{ width, height float64 }
	dims := make(map[int]pageDims)
	for pageNum := 1; pageNum <= pdfCtx.PageCount; pageNum++ {
		_, _, inhPAttrs, err := pdfCtx.XRefTable.PageDict(pageNum, false)
		if err != nil || inhPAttrs.MediaBox == nil {
			continue
		}
		dims[pageNum] = pageDims{inhPAttrs.MediaBox.Width(), inhPAttrs.MediaBox.Height()}
	}

	// Build watermark map: page number -> list of stamps
	wmMap := make(map[int][]*model.Watermark)

	for _, f := range fieldValues {
		pageNum := getIntField(f, "pageNumber")
		if pageNum <= 0 {
			pageNum = 1
		}

		d, ok := dims[pageNum]
		if !ok {
			g.log.Warnf("overlay: no dimensions for page %d", pageNum)
			continue
		}

		fieldType := getStringField(f, "type")
		value := getStringField(f, "value")
		if value == "" {
			continue
		}

		// Convert CSS coords (top-left origin, percentages) to PDF coords (bottom-left origin, points)
		xPct := getFloat64Field(f, "xPercent")
		yPct := getFloat64Field(f, "yPercent")
		hPct := getFloat64Field(f, "heightPercent")
		wPct := getFloat64Field(f, "widthPercent")

		x := xPct / 100.0 * d.width
		fieldH := hPct / 100.0 * d.height
		fieldW := wPct / 100.0 * d.width
		yTop := yPct / 100.0 * d.height
		yBottom := d.height - yTop - fieldH

		if fieldW < 20 {
			fieldW = 120
		}
		if fieldH < 10 {
			fieldH = 20
		}

		isSignature := fieldType == "signature" || fieldType == "initials" ||
			fieldType == "TEMPLATE_FIELD_TYPE_SIGNATURE" || fieldType == "TEMPLATE_FIELD_TYPE_INITIALS"

		if isSignature {
			g.addSignatureWatermark(wmMap, pageNum, value, x, yBottom, fieldW, fieldH)
		} else {
			fontSize := getFloat64Field(f, "font_size")
			if fontSize <= 0 {
				fontSize = fieldH * 0.6
				if fontSize > 12 {
					fontSize = 12
				}
				if fontSize < 6 {
					fontSize = 6
				}
			}

			desc := fmt.Sprintf("font:DejaVuSans, points:%d, position:bl, offset:%.1f %.1f, scalefactor:1 abs, color:0 0 0, opacity:1, rotation:0, margins:2",
				int(fontSize), x, yBottom)
			wm, err := api.TextWatermark(value, desc, true, false, types.POINTS)
			if err != nil {
				g.log.Warnf("overlay: create text watermark for %q: %v", value, err)
				continue
			}
			wmMap[pageNum] = append(wmMap[pageNum], wm)
		}
	}

	if len(wmMap) == 0 {
		return pdfContent, nil
	}

	// Apply all stamps
	var buf bytes.Buffer
	stampConf := model.NewDefaultConfiguration()
	stampConf.ValidationMode = model.ValidationRelaxed
	if err := api.AddWatermarksSliceMap(bytes.NewReader(pdfContent), &buf, wmMap, stampConf); err != nil {
		return nil, fmt.Errorf("add watermarks: %w", err)
	}

	g.log.Infof("overlaid %d field groups on PDF", len(wmMap))
	return buf.Bytes(), nil
}

// addSignatureWatermark adds a signature/initials image watermark to the map.
func (g *PDFGenerator) addSignatureWatermark(wmMap map[int][]*model.Watermark, pageNum int, base64Data string, x, yBottom, fieldW, fieldH float64) {
	imgBytes, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		g.log.Warnf("overlay: decode signature image: %v", err)
		return
	}

	tmpFile, err := os.CreateTemp("", "sig-*.png")
	if err != nil {
		g.log.Warnf("overlay: create temp file: %v", err)
		return
	}
	tmpFile.Write(imgBytes)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		g.log.Warnf("overlay: decode image dimensions: %v", err)
		return
	}

	imgW := float64(img.Bounds().Dx())
	imgH := float64(img.Bounds().Dy())
	scaleX := fieldW / imgW
	scaleY := fieldH / imgH
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}

	drawW := imgW * scale
	drawH := imgH * scale
	offsetX := x + (fieldW-drawW)/2
	offsetY := yBottom + (fieldH-drawH)/2

	desc := fmt.Sprintf("position:bl, offset:%.1f %.1f, scalefactor:%.4f abs, opacity:1, rotation:0",
		offsetX, offsetY, scale)
	wm, err := api.ImageWatermark(tmpFile.Name(), desc, true, false, types.POINTS)
	if err != nil {
		g.log.Warnf("overlay: create image watermark: %v", err)
		return
	}
	wmMap[pageNum] = append(wmMap[pageNum], wm)
}

// groupFieldsByPage groups field values by their page number.
func groupFieldsByPage(fieldValues []map[string]interface{}) map[int][]map[string]interface{} {
	result := make(map[int][]map[string]interface{})
	for _, field := range fieldValues {
		page := getIntField(field, "pageNumber")
		if page <= 0 {
			page = 1
		}
		result[page] = append(result[page], field)
	}
	return result
}

// getStringField safely extracts a string value from a map.
func getStringField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

// getFloat64Field safely extracts a float64 value from a map.
func getFloat64Field(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// getIntField safely extracts an int value from a map.
func getIntField(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// getBoolField safely extracts a bool value from a map.
func getBoolField(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

// getInt32Field safely extracts an int32 value from a map.
func getInt32Field(m map[string]interface{}, key string) int32 {
	return int32(getIntField(m, key))
}

// buildSignatureAppearanceAndOverlayExtras collects all signature/initials fields for a submitter,
// uses the first for the PAdES digital signature appearance, and overlays stamp images on any
// additional signature fields as visual-only watermarks. Returns the PAdES appearance and
// (possibly modified) PDF content.
func buildSignatureAppearanceAndOverlayExtras(
	pdfContent []byte,
	fieldsSnapshot []map[string]interface{},
	signerOrder int,
	signerName, issuerCN string,
	fixedDate time.Time,
) (sign.Appearance, []byte) {
	// Collect all signature fields for this submitter
	type sigField struct {
		xPct, yPct, hPct float64
		pgNum            int
	}
	var fields []sigField
	for _, f := range fieldsSnapshot {
		fType := getStringField(f, "type")
		fIdx := getIntField(f, "submitter_index")
		if (fType == "signature" || fType == "initials") && fIdx == signerOrder {
			pgNum := getIntField(f, "page_number")
			if pgNum <= 0 {
				pgNum = 1
			}
			fields = append(fields, sigField{
				xPct:  getFloat64Field(f, "x_percent"),
				yPct:  getFloat64Field(f, "y_percent"),
				hPct:  getFloat64Field(f, "height_percent"),
				pgNum: pgNum,
			})
		}
	}

	if len(fields) == 0 {
		return sign.Appearance{Visible: false}, pdfContent
	}

	pageW, pageH := detectPageSize(pdfContent)

	// Helper to compute stamp dimensions for a field
	buildStamp := func(sf sigField) (x, yBottom, stampW, h float64, stampImg []byte, pgNum int) {
		x = sf.xPct / 100.0 * pageW
		h = sf.hPct / 100.0 * pageH
		yTop := sf.yPct / 100.0 * pageH

		imgH := int(h) * 3
		if imgH < 80 {
			imgH = 80
		}
		imgW := imgH * 3
		stampImg = generateSignatureStampImage(signerName, issuerCN, fixedDate, imgW, imgH)
		stampW = h * float64(imgW) / float64(imgH)
		yBottom = pageH - yTop - h
		pgNum = sf.pgNum
		return
	}

	// First field: PAdES digital signature appearance
	x, yBottom, stampW, h, stampImg, pgNum := buildStamp(fields[0])
	appearance := sign.Appearance{
		Visible:     true,
		Page:        uint32(pgNum),
		LowerLeftX:  x,
		LowerLeftY:  yBottom,
		UpperRightX: x + stampW,
		UpperRightY: yBottom + h,
		Image:       stampImg,
	}

	// Additional fields: overlay stamp images as pdfcpu watermarks
	if len(fields) > 1 {
		wmMap := make(map[int][]*model.Watermark)

		for _, sf := range fields[1:] {
			ex, eyBottom, _, eh, eStampImg, ePgNum := buildStamp(sf)

			// Write stamp image to temp file for pdfcpu
			tmpFile, err := os.CreateTemp("", "stamp-extra-*.png")
			if err != nil {
				continue
			}
			tmpFile.Write(eStampImg)
			tmpFile.Close()

			// Compute scale: stamp image is rendered at 3x field height
			img, _, err := image.Decode(bytes.NewReader(eStampImg))
			if err != nil {
				os.Remove(tmpFile.Name())
				continue
			}
			imgW := float64(img.Bounds().Dx())
			imgH := float64(img.Bounds().Dy())
			scale := eh / imgH
			drawW := imgW * scale
			offsetX := ex + (eh*3-drawW)/2 // center horizontally in approximate field width
			if offsetX < ex {
				offsetX = ex
			}

			desc := fmt.Sprintf("position:bl, offset:%.1f %.1f, scalefactor:%.4f abs, opacity:1, rotation:0",
				offsetX, eyBottom, scale)
			wm, err := api.ImageWatermark(tmpFile.Name(), desc, true, false, types.POINTS)
			os.Remove(tmpFile.Name())
			if err != nil {
				continue
			}
			wmMap[ePgNum] = append(wmMap[ePgNum], wm)
		}

		if len(wmMap) > 0 {
			var buf bytes.Buffer
			stampConf := model.NewDefaultConfiguration()
			stampConf.ValidationMode = model.ValidationRelaxed
			if err := api.AddWatermarksSliceMap(bytes.NewReader(pdfContent), &buf, wmMap, stampConf); err == nil {
				pdfContent = buf.Bytes()
			}
		}
	}

	return appearance, pdfContent
}
 
