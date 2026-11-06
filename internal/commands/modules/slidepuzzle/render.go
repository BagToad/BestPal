package slidepuzzle

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register decoders for uploaded images
	"image/jpeg"
	_ "image/png"
	"strconv"
	"sync"

	_ "golang.org/x/image/webp"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	// boardMaxSide is the longest side of the rendered board, in pixels.
	boardMaxSide = 960
	// storedMaxSide caps uploaded images before they are stored.
	storedMaxSide = 1280
	// maxSourcePixels guards against decompression bombs in uploads.
	maxSourcePixels = 40_000_000
	tileInset       = 2
	// minImageSide is the smallest stored side; it keeps every tile visible
	// even on the largest grid.
	minImageSide = 64
	// maxAspect caps how elongated an uploaded image may be.
	maxAspect   = 4
	jpegQuality = 85
)

var (
	gapColor    = color.RGBA{0x1e, 0x1f, 0x22, 0xff}
	borderColor = color.RGBA{0x11, 0x12, 0x14, 0xff}
	badgeColor  = color.RGBA{0x00, 0x00, 0x00, 0xc0}
	voteColor   = color.RGBA{0x58, 0x65, 0xf2, 0xe0}

	fontOnce sync.Once
	fontErr  error
	boldFont *opentype.Font
)

// labelFace returns a new face for one render. The parsed font is shared, but
// faces hold glyph buffers and are not safe for concurrent use.
func labelFace(size float64) (font.Face, error) {
	fontOnce.Do(func() { boldFont, fontErr = opentype.Parse(gobold.TTF) })
	if fontErr != nil {
		return nil, fontErr
	}
	return opentype.NewFace(boldFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// decodeImage decodes image bytes, rejecting oversized images before the full
// decode allocates their pixels.
func decodeImage(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a supported image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxSourcePixels {
		return nil, fmt.Errorf("image is %dx%d, which is too large", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not decode image: %w", err)
	}
	return img, nil
}

// fitSize scales w x h so the longest side is maxSide.
func fitSize(w, h, maxSide int) (int, int) {
	if w >= h {
		return maxSide, max(1, h*maxSide/w)
	}
	return max(1, w*maxSide/h), maxSide
}

// normalizeUpload decodes an uploaded image, caps its size and re-encodes it
// as JPEG for storage.
func normalizeUpload(data []byte) ([]byte, error) {
	img, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) > storedMaxSide {
		w, h = fitSize(w, h, storedMaxSide)
	}
	// Checked on the stored size, and with an aspect cap, so long thin images
	// can't enter the pool and later render with unusably thin tiles.
	if min(w, h) < minImageSide {
		return nil, fmt.Errorf("image is %dx%d; it needs to be at least %dpx on each side", b.Dx(), b.Dy(), minImageSide)
	}
	if max(w, h) > maxAspect*min(w, h) {
		return nil, fmt.Errorf("image is %dx%d; it can't be more than %d times longer than it is wide", b.Dx(), b.Dy(), maxAspect)
	}
	if w != b.Dx() || h != b.Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("could not encode image: %w", err)
	}
	return buf.Bytes(), nil
}

// renderBoard draws the board as a JPEG: each tile is cut from its home spot
// in the source image and drawn at its current spot with its number in the
// corner. Tiles listed in votes (the ones that can move this round) also get
// their current vote count in the opposite corner. A solved board is drawn as
// the complete picture.
func renderBoard(src image.Image, grid int, board []int, solved bool, votes map[int]int) ([]byte, error) {
	sb := src.Bounds()
	w, h := fitSize(sb.Dx(), sb.Dy(), boardMaxSide)
	w, h = w-w%grid, h-h%grid
	if w/grid < 4*tileInset || h/grid < 4*tileInset {
		return nil, fmt.Errorf("image is too small for a %dx%d grid", grid, grid)
	}
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, sb, xdraw.Src, nil)

	if !solved {
		tw, th := w/grid, h/grid
		out := image.NewRGBA(scaled.Bounds())
		xdraw.Draw(out, out.Bounds(), image.NewUniform(borderColor), image.Point{}, xdraw.Src)
		face, err := labelFace(float64(max(12, min(tw, th)/4)))
		if err != nil {
			return nil, fmt.Errorf("load font: %w", err)
		}
		defer face.Close()
		voteFace, err := labelFace(float64(max(11, min(tw, th)/5)))
		if err != nil {
			return nil, fmt.Errorf("load font: %w", err)
		}
		defer voteFace.Close()
		for pos, tile := range board {
			cell := image.Rect((pos%grid)*tw, (pos/grid)*th, (pos%grid+1)*tw, (pos/grid+1)*th).Inset(tileInset)
			if tile == 0 {
				xdraw.Draw(out, cell, image.NewUniform(gapColor), image.Point{}, xdraw.Src)
				continue
			}
			home := tile - 1
			srcPt := image.Pt((home%grid)*tw+tileInset, (home/grid)*th+tileInset)
			xdraw.Draw(out, cell, scaled, srcPt, xdraw.Src)
			drawBadge(out, cell, face, strconv.Itoa(tile))
			if n, ok := votes[tile]; ok {
				drawVoteBadge(out, cell, voteFace, n)
			}
		}
		scaled = out
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode board: %w", err)
	}
	return buf.Bytes(), nil
}

// drawBadge draws a number on a translucent box in the cell's top-left corner.
func drawBadge(dst *image.RGBA, cell image.Rectangle, face font.Face, text string) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(color.White), Face: face}
	m := face.Metrics()
	tw := d.MeasureString(text).Ceil()
	th := (m.Ascent + m.Descent).Ceil()
	pad := max(3, th/5)
	box := image.Rect(cell.Min.X, cell.Min.Y, cell.Min.X+tw+2*pad, cell.Min.Y+th+pad).Intersect(cell)
	xdraw.Draw(dst, box, image.NewUniform(badgeColor), image.Point{}, xdraw.Over)
	d.Dot = fixed.P(cell.Min.X+pad, cell.Min.Y+pad/2+m.Ascent.Ceil())
	d.DrawString(text)
}

// voteLabel is the vote badge text, shortened when the long form won't fit.
func voteLabel(face font.Face, n, maxWidth int) string {
	long := fmt.Sprintf("%d votes", n)
	if n == 1 {
		long = "1 vote"
	}
	if (&font.Drawer{Face: face}).MeasureString(long).Ceil() <= maxWidth {
		return long
	}
	return strconv.Itoa(n)
}

// drawVoteBadge draws the tile's vote count in the cell's bottom-right corner.
func drawVoteBadge(dst *image.RGBA, cell image.Rectangle, face font.Face, n int) {
	m := face.Metrics()
	th := (m.Ascent + m.Descent).Ceil()
	pad := max(3, th/5)
	text := voteLabel(face, n, cell.Dx()-2*pad)
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(color.White), Face: face}
	tw := d.MeasureString(text).Ceil()
	box := image.Rect(cell.Max.X-tw-2*pad, cell.Max.Y-th-pad, cell.Max.X, cell.Max.Y).Intersect(cell)
	xdraw.Draw(dst, box, image.NewUniform(voteColor), image.Point{}, xdraw.Over)
	d.Dot = fixed.P(box.Min.X+pad, box.Min.Y+pad/2+m.Ascent.Ceil())
	d.DrawString(text)
}
