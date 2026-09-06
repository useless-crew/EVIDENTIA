package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/utils"
)

func TestValidateRedactionReason(t *testing.T) {
	t.Run("trims and accepts a real reason", func(t *testing.T) {
		got, err := validateRedactionReason("  Protect witness identity  ")
		require.NoError(t, err)
		assert.Equal(t, "Protect witness identity", got)
	})

	t.Run("rejects empty", func(t *testing.T) {
		_, err := validateRedactionReason("")
		require.Error(t, err)
		appErr, ok := utils.AsAppError(err)
		require.True(t, ok)
		assert.Equal(t, 400, appErr.Status)
	})

	t.Run("rejects whitespace-only", func(t *testing.T) {
		_, err := validateRedactionReason("   ")
		require.Error(t, err)
	})

	t.Run("rejects too short", func(t *testing.T) {
		_, err := validateRedactionReason("ab")
		require.Error(t, err)
	})

	t.Run("rejects too long", func(t *testing.T) {
		_, err := validateRedactionReason(strings.Repeat("a", maxRedactionReasonLen+1))
		require.Error(t, err)
	})

	t.Run("rejects invalid UTF-8", func(t *testing.T) {
		_, err := validateRedactionReason(string([]byte{0xff, 0xfe, 0xfd}))
		require.Error(t, err)
	})
}

func TestValidateRedactionRegions(t *testing.T) {
	t.Run("rejects empty region list", func(t *testing.T) {
		_, err := validateRedactionRegions(nil)
		require.Error(t, err)
		appErr, ok := utils.AsAppError(err)
		require.True(t, ok)
		assert.Equal(t, 400, appErr.Status)
	})

	t.Run("rejects too many regions", func(t *testing.T) {
		regions := make([]RedactRegion, maxRedactionRegions+1)
		for i := range regions {
			regions[i] = RedactRegion{Page: 1, X: 0, Y: 0, Width: 1, Height: 1}
		}
		_, err := validateRedactionRegions(regions)
		require.Error(t, err)
	})

	t.Run("rejects negative coordinates", func(t *testing.T) {
		_, err := validateRedactionRegions([]RedactRegion{{Page: 1, X: -1, Y: 0, Width: 10, Height: 10}})
		require.Error(t, err)
	})

	t.Run("rejects non-positive dimensions", func(t *testing.T) {
		_, err := validateRedactionRegions([]RedactRegion{{Page: 1, X: 0, Y: 0, Width: 0, Height: 10}})
		require.Error(t, err)

		_, err = validateRedactionRegions([]RedactRegion{{Page: 1, X: 0, Y: 0, Width: 10, Height: -5}})
		require.Error(t, err)
	})

	t.Run("rejects page less than 1", func(t *testing.T) {
		_, err := validateRedactionRegions([]RedactRegion{{Page: 0, X: 0, Y: 0, Width: 10, Height: 10}})
		require.Error(t, err)
	})

	t.Run("rejects non-finite coordinates", func(t *testing.T) {
		_, err := validateRedactionRegions([]RedactRegion{{Page: 1, X: math.NaN(), Y: 0, Width: 10, Height: 10}})
		require.Error(t, err)
	})

	t.Run("accepts a valid region list", func(t *testing.T) {
		got, err := validateRedactionRegions([]RedactRegion{{Page: 1, X: 1, Y: 2, Width: 10, Height: 20}})
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})
}

// solidColorImage builds a small, fully-opaque test image with every pixel
// set to c — deterministic content a test can assert was (or was not)
// destroyed by redaction.
func solidColorImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestApplyRedactions_ActuallyOverwritesPixels(t *testing.T) {
	sensitive := color.NRGBA{R: 200, G: 50, B: 50, A: 255}
	src := solidColorImage(20, 20, sensitive)

	redacted := applyRedactions(src, []RedactRegion{{Page: 1, X: 5, Y: 5, Width: 10, Height: 10}})

	// Inside the redacted region: must be opaque black everywhere, not the
	// original sensitive color, and not merely alpha-blended (draw.Src is a
	// straight replace).
	for y := 5; y < 15; y++ {
		for x := 5; x < 15; x++ {
			r, g, b, a := redacted.At(x, y).RGBA()
			assert.Equal(t, uint32(0), r, "x=%d y=%d red channel must be fully zeroed", x, y)
			assert.Equal(t, uint32(0), g, "x=%d y=%d green channel must be fully zeroed", x, y)
			assert.Equal(t, uint32(0), b, "x=%d y=%d blue channel must be fully zeroed", x, y)
			assert.Equal(t, uint32(0xffff), a, "x=%d y=%d must remain fully opaque (no transparency trick)", x, y)
		}
	}

	// Outside the region: the original content must be untouched.
	r, g, b, _ := redacted.At(0, 0).RGBA()
	assert.Equal(t, uint32(sensitive.R)*0x101, r)
	assert.Equal(t, uint32(sensitive.G)*0x101, g)
	assert.Equal(t, uint32(sensitive.B)*0x101, b)

	// The source image itself must never be mutated.
	sr, _, _, _ := src.At(7, 7).RGBA()
	assert.Equal(t, uint32(sensitive.R)*0x101, sr, "applyRedactions must not mutate its input image")
}

func TestApplyRedactions_RoundsOutward_NoSliverLeftUnredacted(t *testing.T) {
	sensitive := color.NRGBA{R: 9, G: 9, B: 9, A: 255}
	src := solidColorImage(10, 10, sensitive)

	// A fractional region: 2.4..7.6 must fully cover every pixel it
	// overlaps at all — floor(2.4)=2 through ceil(7.6)=8 (exclusive), i.e.
	// pixels 2..7 — rounding OUTWARD rather than truncating inward, so no
	// edge pixel of the original content survives unredacted.
	redacted := applyRedactions(src, []RedactRegion{{Page: 1, X: 2.4, Y: 2.4, Width: 5.2, Height: 5.2}})

	for _, p := range [][2]int{{2, 2}, {7, 7}, {2, 7}, {7, 2}} {
		r, _, _, _ := redacted.At(p[0], p[1]).RGBA()
		assert.Equal(t, uint32(0), r, "pixel (%d,%d), inside the requested region, must be fully redacted", p[0], p[1])
	}

	// Just outside the requested region: original content must survive.
	r, _, _, _ := redacted.At(1, 1).RGBA()
	assert.Equal(t, uint32(sensitive.R)*0x101, r, "pixel (1,1), outside the requested region, must be untouched")
}

func TestRedactedFilename(t *testing.T) {
	assert.Equal(t, "redacted_statement.pdf", redactedFilename("statement.pdf"))

	long := strings.Repeat("a", maxDocumentFilenameLen)
	got := redactedFilename(long)
	assert.LessOrEqual(t, len(got), maxDocumentFilenameLen)
}

func TestSha256Sum_DiffersOnDifferentContent(t *testing.T) {
	a := sha256Sum([]byte("one"))
	b := sha256Sum([]byte("two"))
	assert.NotEqual(t, a, b)
	assert.Len(t, a, 32)
}

// fakeGetOnlyStorage implements storage.Storage with only Get behaving
// meaningfully — enough to unit-test readAllLimited without a real MinIO.
type fakeGetOnlyStorage struct {
	data []byte
	err  error
}

func (f *fakeGetOnlyStorage) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (f *fakeGetOnlyStorage) Get(context.Context, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}
func (f *fakeGetOnlyStorage) Delete(context.Context, string) error         { return nil }
func (f *fakeGetOnlyStorage) Exists(context.Context, string) (bool, error) { return true, nil }
func (f *fakeGetOnlyStorage) HealthCheck(context.Context) error            { return nil }

func TestReadAllLimited(t *testing.T) {
	t.Run("reads content within the limit", func(t *testing.T) {
		fake := &fakeGetOnlyStorage{data: []byte("small content")}
		got, err := readAllLimited(context.Background(), fake, "some/key", 1024)
		require.NoError(t, err)
		assert.Equal(t, []byte("small content"), got)
	})

	t.Run("rejects content over the limit without buffering it whole", func(t *testing.T) {
		fake := &fakeGetOnlyStorage{data: bytes.Repeat([]byte("x"), 100)}
		_, err := readAllLimited(context.Background(), fake, "some/key", 10)
		require.Error(t, err)
	})

	t.Run("propagates a storage retrieval error", func(t *testing.T) {
		wantErr := errors.New("boom")
		fake := &fakeGetOnlyStorage{err: wantErr}
		_, err := readAllLimited(context.Background(), fake, "some/key", 10)
		require.Error(t, err)
	})
}

// encodePNG is a small test helper producing real PNG bytes so
// integration tests can exercise image.Decode end-to-end.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
