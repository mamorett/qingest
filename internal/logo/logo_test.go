package logo

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/blacktop/go-termimg"
)

func createTestPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
			} else {
				img.Set(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDetectBestProtocol(t *testing.T) {
	proto := DetectBestProtocol()
	if proto == termimg.Unsupported {
		t.Errorf("Expected supported protocol or fallback")
	}
}

func TestPrintLogoToNonTerminal(t *testing.T) {
	pngData := createTestPNG()
	var buf bytes.Buffer

	// Buffer is not an *os.File, so PrintLogoTo should return cleanly without error
	PrintLogoTo(&buf, pngData)
}

func TestPrintTransparentHalfblocksTo(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255}) // top opaque
	img.Set(0, 1, color.NRGBA{0, 0, 0, 0})     // bot transparent
	img.Set(1, 0, color.NRGBA{0, 0, 0, 0})     // top transparent
	img.Set(1, 1, color.NRGBA{0, 255, 0, 255}) // bot opaque
	img.Set(2, 0, color.NRGBA{0, 0, 255, 255}) // top opaque
	img.Set(2, 1, color.NRGBA{255, 255, 0, 255}) // bot opaque
	img.Set(3, 0, color.NRGBA{0, 0, 0, 0})     // top transparent
	img.Set(3, 1, color.NRGBA{0, 0, 0, 0})     // bot transparent

	var buf bytes.Buffer
	PrintTransparentHalfblocksTo(&buf, img, 4, 2)

	output := buf.String()
	if len(output) == 0 {
		t.Errorf("Expected non-empty halfblocks output")
	}
}

func TestPrintITerm2PNGTo(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	var buf bytes.Buffer
	err := PrintITerm2PNGTo(&buf, img, 10, 5)
	if err != nil {
		t.Fatalf("PrintITerm2PNGTo returned error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("1337;File=inline=1")) {
		t.Errorf("Expected iTerm2 escape sequence in output, got: %s", output)
	}
}
