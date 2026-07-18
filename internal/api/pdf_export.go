package api

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"strings"
)

// imageToJPEG re-encodes any source image as plain RGB JPEG, so every PDF
// page uses the same /DeviceRGB colorspace regardless of source format.
func imageToJPEG(data []byte) (jpegBytes []byte, width, height int, err error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 92}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), b.Dx(), b.Dy(), nil
}

type pdfPage struct {
	jpegData      []byte
	width, height int
}

// buildImagesPDF hand-rolls a minimal PDF (no external tool/library needed):
// one full-page image per page, MediaBox sized 1:1 to the image's own pixels.
func buildImagesPDF(pages []pdfPage) []byte {
	n := len(pages)
	const catalogNum = 1
	const pagesNum = 2
	totalObjs := 2 + 3*n // per page: page, content stream, image XObject

	var buf bytes.Buffer
	offsets := make([]int, totalObjs+1) // 1-indexed; index 0 unused

	buf.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")

	writeObj := func(num int, header string, stream []byte) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s", num, header)
		if stream != nil {
			buf.WriteString("\nstream\n")
			buf.Write(stream)
			buf.WriteString("\nendstream")
		}
		buf.WriteString("\nendobj\n")
	}

	pageRefs := make([]string, n)
	for i := range pageRefs {
		pageRefs[i] = fmt.Sprintf("%d 0 R", 3+3*i)
	}
	writeObj(catalogNum, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesNum), nil)
	writeObj(pagesNum, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(pageRefs, " "), n), nil)

	for i, p := range pages {
		pageNum := 3 + 3*i
		contentNum := 4 + 3*i
		imageNum := 5 + 3*i

		content := []byte(fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", p.width, p.height))
		writeObj(contentNum, fmt.Sprintf("<< /Length %d >>", len(content)), content)

		writeObj(pageNum, fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			pagesNum, p.width, p.height, imageNum, contentNum,
		), nil)

		writeObj(imageNum, fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>",
			p.width, p.height, len(p.jpegData),
		), p.jpegData)
	}

	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", totalObjs+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= totalObjs; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF", totalObjs+1, catalogNum, xrefStart)

	return buf.Bytes()
}
