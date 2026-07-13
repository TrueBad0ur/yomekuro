package main

import (
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// A MokuroVolume for a PDF with a text layer: same shape as an OCR result, but
// coordinates come from `pdftotext -bbox-layout`. volDir holds the raster pages.
func buildTextVolume(pdfPath, volDir, volumeName string) (MokuroVolume, error) {
	pages, err := parsePDFTextLayout(pdfPath)
	if err != nil {
		return MokuroVolume{}, fmt.Errorf("parse text layout: %w", err)
	}

	images, err := sortedImages(volDir)
	if err != nil {
		return MokuroVolume{}, fmt.Errorf("list page images: %w", err)
	}
	if len(images) != len(pages) {
		return MokuroVolume{}, fmt.Errorf("page count mismatch: %d rendered images, %d pdf pages", len(images), len(pages))
	}

	uuid, err := newUUID()
	if err != nil {
		return MokuroVolume{}, err
	}

	vol := MokuroVolume{
		Volume:     volumeName,
		VolumeUUID: uuid,
		Pages:      make([]MokuroPage, len(pages)),
	}

	for i, p := range pages {
		imgW, imgH, err := imageDimensions(filepath.Join(volDir, images[i]))
		if err != nil {
			return MokuroVolume{}, fmt.Errorf("read image %s: %w", images[i], err)
		}
		// bbox coords are PDF points: scale to the actual rendered pixel size, not
		// to pdftoppm's DPI, so the two can't drift apart.
		scaleX, scaleY := float64(imgW)/p.Width, float64(imgH)/p.Height

		vol.Pages[i] = MokuroPage{
			ImgPath:   images[i],
			ImgWidth:  imgW,
			ImgHeight: imgH,
			Blocks:    p.blocks(scaleX, scaleY),
		}
	}

	return vol, nil
}

func sortedImages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func imageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// ── pdftotext -bbox-layout parsing ──────────────────────────────────────────

// parsePDFTextLayout runs `pdftotext -bbox-layout` and returns one entry per
// page with each line's text and position, in PDF point units.
func parsePDFTextLayout(pdfPath string) ([]bboxPage, error) {
	out, err := exec.Command("pdftotext", "-bbox-layout", pdfPath, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext -bbox-layout: %w", err)
	}
	var doc bboxHTML
	if err := xml.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse bbox xml: %w", err)
	}
	return doc.Body.Doc.Pages, nil
}

type bboxHTML struct {
	Body struct {
		Doc struct {
			Pages []bboxPage `xml:"page"`
		} `xml:"doc"`
	} `xml:"body"`
}

type bboxPage struct {
	Width  float64    `xml:"width,attr"`
	Height float64    `xml:"height,attr"`
	Flows  []bboxFlow `xml:"flow"`
}

type bboxFlow struct {
	Blocks []bboxBlock `xml:"block"`
}

type bboxBlock struct {
	Lines []bboxLine `xml:"line"`
}

type bboxLine struct {
	XMin  float64    `xml:"xMin,attr"`
	YMin  float64    `xml:"yMin,attr"`
	XMax  float64    `xml:"xMax,attr"`
	YMax  float64    `xml:"yMax,attr"`
	Words []bboxWord `xml:"word"`
}

type bboxWord struct {
	Text string `xml:",chardata"`
}

// Converts poppler's flow/block/line tree into MokuroBlocks, scaling PDF points
// to pixels. Each poppler block maps 1:1 to a MokuroBlock, as mokuro groups too.
func (p bboxPage) blocks(scaleX, scaleY float64) []MokuroBlock {
	var out []MokuroBlock
	for _, flow := range p.Flows {
		for _, blk := range flow.Blocks {
			if len(blk.Lines) == 0 {
				continue
			}
			mb := MokuroBlock{
				Lines:       make([]string, len(blk.Lines)),
				LinesCoords: make([][][]float64, len(blk.Lines)),
			}
			minX, minY := blk.Lines[0].XMin*scaleX, blk.Lines[0].YMin*scaleY
			maxX, maxY := minX, minY
			for i, line := range blk.Lines {
				text := ""
				for _, w := range line.Words {
					if text != "" {
						text += " "
					}
					text += w.Text
				}
				x1, y1 := line.XMin*scaleX, line.YMin*scaleY
				x2, y2 := line.XMax*scaleX, line.YMax*scaleY
				mb.Lines[i] = text
				mb.LinesCoords[i] = [][]float64{{x1, y1}, {x2, y2}}
				// Vertical text gives narrow-tall line boxes, horizontal wide-short.
				// pdftotext offers no script hint, so aspect ratio is all there is.
				if y2-y1 > x2-x1 {
					mb.Vertical = true
				}
				minX, minY = min(minX, x1), min(minY, y1)
				maxX, maxY = max(maxX, x2), max(maxY, y2)
			}
			mb.Box = [4]int{iround(minX), iround(minY), iround(maxX), iround(maxY)}
			out = append(out, mb)
		}
	}
	return out
}

func newUUID() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", err
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]), nil
}
