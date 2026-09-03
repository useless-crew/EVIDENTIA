package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeFilename_StripsPathTraversal(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":   "passwd",
		"..\\..\\secret.txt": "secret.txt",
		"/etc/passwd":        "passwd",
		"a/b/c/report.pdf":   "report.pdf",
	}
	for input, wantSuffix := range cases {
		got := sanitizeFilename(input)
		assert.NotContains(t, got, "..", "input %q must not leave any traversal sequence", input)
		assert.NotContains(t, got, "/")
		assert.NotContains(t, got, "\\")
		assert.Equal(t, wantSuffix, got, "input %q", input)
	}
}

func TestSanitizeFilename_StripsControlCharsIncludingCRLF(t *testing.T) {
	got := sanitizeFilename("evil\r\nX-Injected: true.txt")
	assert.NotContains(t, got, "\r")
	assert.NotContains(t, got, "\n")
}

func TestSanitizeFilename_EmptyOrOnlyTraversalFallsBackToDefault(t *testing.T) {
	assert.Equal(t, "document", sanitizeFilename(""))
	assert.Equal(t, "document", sanitizeFilename("   "))
	assert.Equal(t, "document", sanitizeFilename("../"))
}

func TestSanitizeFilename_TruncatesExcessiveLength(t *testing.T) {
	got := sanitizeFilename(strings.Repeat("a", 1000) + ".txt")
	assert.LessOrEqual(t, len(got), maxDocumentFilenameLen)
}

func TestSanitizeFilename_PreservesOrdinaryFilename(t *testing.T) {
	assert.Equal(t, "FIR-2026-001.pdf", sanitizeFilename("FIR-2026-001.pdf"))
}

func TestDocumentObjectKey_IsServerGeneratedAndDeterministic(t *testing.T) {
	caseID := uuid.New()
	documentID := uuid.New()

	key := documentObjectKey(caseID, documentID)
	assert.Equal(t, "cases/"+caseID.String()+"/documents/"+documentID.String()+"/original", key)
	assert.NotContains(t, key, "..")
}

func TestDocumentObjectKey_DifferentDocumentsNeverCollide(t *testing.T) {
	caseID := uuid.New()
	key1 := documentObjectKey(caseID, uuid.New())
	key2 := documentObjectKey(caseID, uuid.New())
	assert.NotEqual(t, key1, key2)
}

func TestLimitedReader_AllowsExactlyLimitBytes(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 100)
	lr := &limitedReader{r: bytes.NewReader(content), limit: 100}

	got, err := io.ReadAll(lr)
	require.NoError(t, err)
	assert.Equal(t, content, got)
	assert.False(t, lr.exceeded)
}

func TestLimitedReader_RejectsOneByteOverLimit(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 101)
	lr := &limitedReader{r: bytes.NewReader(content), limit: 100}

	_, err := io.ReadAll(lr)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUploadTooLarge)
	assert.True(t, lr.exceeded)
}

func TestSniffContentType_DetectsFromContentNotExtension(t *testing.T) {
	// A PNG magic-number prefix, regardless of what a client-declared
	// Content-Type or filename extension might claim.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	contentType, rest, err := sniffContentType(bytes.NewReader(png))
	require.NoError(t, err)
	assert.Equal(t, "image/png", contentType)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, png, got, "no bytes may be lost or duplicated by the sniff-then-continue split")
}

func TestSniffContentType_HandlesFileSmallerThanSniffWindow(t *testing.T) {
	small := []byte("hi")
	contentType, rest, err := sniffContentType(bytes.NewReader(small))
	require.NoError(t, err)
	assert.NotEmpty(t, contentType)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, small, got)
}

func TestSniffContentType_PreservesFullContentAcrossSniffBoundary(t *testing.T) {
	// Content longer than the sniff window (sniffLen) must still be
	// delivered in full and in order via rest.
	content := bytes.Repeat([]byte("evidence-bytes-"), 100) // > 512 bytes
	require.Greater(t, len(content), sniffLen)

	_, rest, err := sniffContentType(bytes.NewReader(content))
	require.NoError(t, err)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestSniffContentType_UsesRealHTTPDetection(t *testing.T) {
	// Cross-check against the same stdlib function directly, proving this
	// helper doesn't reinvent (and potentially diverge from) MIME sniffing.
	content := []byte("%PDF-1.4 fake pdf header")
	want := http.DetectContentType(content)

	got, rest, err := sniffContentType(bytes.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, _ = io.ReadAll(rest) // drain, no assertion needed here
}
