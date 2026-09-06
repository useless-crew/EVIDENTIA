//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres AND minio services up, migrated
// (including 000003_certificate_integrity), seeded. See
// document_service_integration_test.go for the shared helpers
// (newDocumentServiceForTest, mustSeedCase, mustAddCaseMember,
// testDocumentStorage) this file reuses.
//
// document:redact is granted ONLY to ADMIN in the seeded role_permissions
// matrix today (see db/seed/001_reference_data.sql and
// backend/tests/rbac_test.go's TestRBAC_PolicePermissions, which asserts
// exactly this) — an existing System 4 policy decision, not something
// this file changes (master prompt §14: "Do not grant new permissions
// merely because System 8 requires redaction"). Every test below that
// performs a real redaction therefore acts as ADMIN; POLICE only ever
// appears here as the document's uploader/owner.
package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/models"
)

// redactTestPNG builds a small, real PNG whose top-left quadrant is a
// distinct "sensitive" color and whose bottom-right quadrant is a
// different "public" color — real, decodable image bytes (not a fixture
// file), same convention as document_redact_test.go's solidColorImage.
func redactTestPNG(t *testing.T) (data []byte, sensitive color.NRGBA) {
	t.Helper()
	sensitive = color.NRGBA{R: 220, G: 20, B: 60, A: 255}
	public := color.NRGBA{R: 30, G: 144, B: 255, A: 255}

	img := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			if x < 20 && y < 20 {
				img.SetNRGBA(x, y, sensitive)
			} else {
				img.SetNRGBA(x, y, public)
			}
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes(), sensitive
}

func redactTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}))
	return buf.Bytes()
}

// TestRedactDocument_OriginalImmutability is master prompt §35's mandatory
// test, verbatim: upload, record H1, redact, re-fetch the original and
// prove its hash/bytes are bit-for-bit unchanged, then prove the
// derivative is a genuinely distinct document with its own hash and a
// lineage pointer back to the source.
func TestRedactDocument_OriginalImmutability(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, objStorage := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer1@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin1@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-1", officer)

	pngBytes, _ := redactTestPNG(t)
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeWitnessStatement,
		Filename:     "witness.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)
	h1 := original.Sha256Hash

	rec.events = nil
	summary, err := svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Protect witness identity",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 20, Height: 20}},
	})
	require.NoError(t, err)
	assert.Contains(t, rec.actions(), "DOCUMENT_REDACTED")

	// 1-6: re-fetch the ORIGINAL and prove it is completely unchanged.
	reFetched, err := svc.DownloadDocument(ctx, authUser(officer, models.RolePolice), original.ID)
	require.NoError(t, err)
	defer reFetched.Content.Close()
	gotBytes, err := io.ReadAll(reFetched.Content)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, gotBytes, "the original object's bytes must be bit-for-bit unchanged after redaction")
	assert.Equal(t, h1, hex.EncodeToString(reFetched.Document.Sha256Hash), "the original's canonical hash must never change")
	assert.Equal(t, models.DocumentStatusActive, reFetched.Document.Status)

	// 7-10: the derivative is a genuinely distinct document.
	assert.NotEqual(t, original.ID, summary.Document.ID, "the derivative must have its own document ID")
	assert.NotEqual(t, h1, summary.Document.Sha256Hash, "the derivative's hash must differ from the original's (H1 != H2)")
	require.NotNil(t, summary.Document.ParentDocumentID)
	assert.Equal(t, original.ID, *summary.Document.ParentDocumentID, "the derivative must point back to the original")
	assert.Equal(t, original.ID, summary.SourceDocumentID)

	// The derivative object actually exists in storage under its own key,
	// distinct from the original's.
	derivativeKey := "cases/" + caseID.String() + "/documents/" + summary.Document.ID.String() + "/original"
	stored, err := objStorage.Get(ctx, derivativeKey)
	require.NoError(t, err)
	defer stored.Close()
	derivativeBytes, err := io.ReadAll(stored)
	require.NoError(t, err)
	assert.NotEqual(t, pngBytes, derivativeBytes, "the derivative's bytes must differ from the original's")

	// The original object at its OWN key is still there, untouched.
	originalKey := "cases/" + caseID.String() + "/documents/" + original.ID.String() + "/original"
	origStored, err := objStorage.Get(ctx, originalKey)
	require.NoError(t, err)
	defer origStored.Close()
	origBytes, err := io.ReadAll(origStored)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, origBytes)
}

// TestRedactDocument_HashIntegrity is master prompt §34: after redaction,
// independently recompute BOTH the original's and the derivative's
// SHA-256 from their actual stored objects and confirm each still matches
// its own canonical hash.
func TestRedactDocument_HashIntegrity(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer2@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin2@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-2", officer)

	pngBytes, _ := redactTestPNG(t)
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "evidence.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	summary, err := svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Remove confidential information",
		Regions: []RedactRegion{{Page: 1, X: 5, Y: 5, Width: 10, Height: 10}},
	})
	require.NoError(t, err)

	origVerify, err := svc.VerifyDocument(ctx, authUser(officer, models.RolePolice), original.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, origVerify.Status, "original must still independently verify")

	derivVerify, err := svc.VerifyDocument(ctx, authUser(officer, models.RolePolice), summary.Document.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, derivVerify.Status, "derivative must independently verify against its OWN hash")
	assert.NotEqual(t, origVerify.StoredHash, derivVerify.StoredHash)
}

// TestRedactDocument_ContentActuallyRemoved is master prompt §36: the
// redacted region's actual pixel content must not be recoverable through
// normal viewing/extraction of the derivative — not merely covered by a
// visual overlay.
func TestRedactDocument_ContentActuallyRemoved(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer3@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin3@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-3", officer)

	pngBytes, sensitive := redactTestPNG(t)
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeWitnessStatement,
		Filename:     "witness2.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	// The sensitive quadrant is [0,20)x[0,20) — redact exactly that.
	summary, err := svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Protect witness identity",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 20, Height: 20}},
	})
	require.NoError(t, err)

	downloaded, err := svc.DownloadDocument(ctx, authUser(officer, models.RolePolice), summary.Document.ID)
	require.NoError(t, err)
	defer downloaded.Content.Close()
	derivativeBytes, err := io.ReadAll(downloaded.Content)
	require.NoError(t, err)

	decoded, _, err := image.Decode(bytes.NewReader(derivativeBytes))
	require.NoError(t, err)

	// Every pixel actually decoded from the derivative's own bytes inside
	// the redacted region must be black — the sensitive color must not be
	// recoverable by decoding the file normally.
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			r, g, b, _ := decoded.At(x, y).RGBA()
			require.Equalf(t, uint32(0), r, "pixel (%d,%d) red channel must not retain the redacted content", x, y)
			require.Equalf(t, uint32(0), g, "pixel (%d,%d) green channel must not retain the redacted content", x, y)
			require.Equalf(t, uint32(0), b, "pixel (%d,%d) blue channel must not retain the redacted content", x, y)
		}
	}

	// Outside the region, the (non-sensitive) public content survives.
	r, _, _, _ := decoded.At(30, 30).RGBA()
	assert.NotEqual(t, uint32(0), r)

	// Sanity: prove the sensitive color really was present in the
	// ORIGINAL (so the assertion above is meaningful, not vacuous).
	origDecoded, _, err := image.Decode(bytes.NewReader(pngBytes))
	require.NoError(t, err)
	or, og, ob, _ := origDecoded.At(5, 5).RGBA()
	assert.Equal(t, uint32(sensitive.R)*0x101, or)
	assert.Equal(t, uint32(sensitive.G)*0x101, og)
	assert.Equal(t, uint32(sensitive.B)*0x101, ob)
}

func TestRedactDocument_JPEGSupported(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer4@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin4@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-4", officer)

	jpegBytes := redactTestJPEG(t)
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypePhotoEvidence,
		Filename:     "photo.jpg",
		File:         bytes.NewReader(jpegBytes),
	})
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", original.MimeType)

	summary, err := svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Remove confidential information",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 10, Height: 10}},
	})
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", summary.Document.MimeType)
}

func TestRedactDocument_UnsupportedFormatRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer5@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin5@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-5", officer)

	// Plain text sniffs to text/plain; UploadDocument accepts any content
	// type (System 6 does not gate uploads by mime_type), but redaction
	// must refuse it.
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeFIR,
		Filename:     "fir.txt",
		File:         bytes.NewReader([]byte("This is a plain text FIR, not an image.")),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Attempt redaction on unsupported format",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 422, appErr.Status)

	// No derivative/redaction row was created.
	var count int
	require.NoError(t, migrator.QueryRow(ctx, `SELECT count(*) FROM redactions WHERE source_document_id = $1`, original.ID).Scan(&count))
	assert.Equal(t, 0, count)
}

func TestRedactDocument_EmptyRegionsRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer6@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin6@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-6", officer)

	pngBytes, _ := redactTestPNG(t)
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "No regions supplied",
		Regions: nil,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestRedactDocument_OutOfBoundsRegionRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer7@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin7@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-7", officer)

	pngBytes, _ := redactTestPNG(t) // 40x40
	original, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Region far outside the actual image bounds",
		Regions: []RedactRegion{{Page: 1, X: 1000, Y: 1000, Width: 50, Height: 50}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

// TestRedactDocument_PoliceDenied: POLICE holds document:upload/download/
// verify/share but NOT document:redact in the seeded role_permissions
// matrix (only ADMIN does — see this file's package doc comment and
// backend/tests/rbac_test.go's TestRBAC_PolicePermissions) — master
// prompt §14/§37's "unauthorized Police user cannot redact". Denied at
// the RBAC layer regardless of case relationship, which this test also
// deliberately sets up correctly (officerB IS a case member) to prove the
// denial is really about the permission, not an incidental case mismatch.
func TestRedactDocument_PoliceDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "redact-officerA8@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "redact-officerB8@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-8", officerA)
	mustAddCaseMember(t, migrator, caseID, officerB, officerA, models.MembershipTypeInvestigator)

	pngBytes, _ := redactTestPNG(t)
	doc, err := svc.UploadDocument(ctx, authUser(officerA, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "a.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(officerB, models.RolePolice), doc.ID, RedactDocumentInput{
		Reason:  "Police attempting redaction",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "POLICE holds no document:redact permission, even as a genuine member of the document's case")
}

func TestRedactDocument_GuessedUUIDDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})

	admin := newUserWithRole(t, migrator, "redact-admin9@example.com", models.RoleAdmin)

	_, err := svc.RedactDocument(context.Background(), authUser(admin, models.RoleAdmin), uuid.New(), RedactDocumentInput{
		Reason:  "Guessed ID",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "even ADMIN gets a generic denial for a document ID that does not exist at all")
}

// TestRedactDocument_LawyerDenied: LAWYER holds no document:redact
// permission in the seeded role_permissions matrix (only ADMIN does) —
// master prompt §14/§37.
func TestRedactDocument_LawyerDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer10@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "redact-lawyer10@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-10", officer)
	mustAddCaseMember(t, migrator, caseID, lawyer, officer, models.MembershipTypeLawyer)

	pngBytes, _ := redactTestPNG(t)
	doc, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(lawyer, models.RoleLawyer), doc.ID, RedactDocumentInput{
		Reason:  "Lawyer attempting redaction",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "LAWYER holds no document:redact permission even when attached to the case")
}

func TestRedactDocument_AdminAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "redact-officer11@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin11@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "REDACT-CASE-11", officer)

	pngBytes, _ := redactTestPNG(t)
	doc, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	_, err = svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), doc.ID, RedactDocumentInput{
		Reason:  "Admin redaction",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.NoError(t, err, "ADMIN can redact any document per policy — the only role granted document:redact today")
}

// TestRedactDocument_DerivativeAccessIndependentlyControlled is master
// prompt §17/§38: a user unrelated to the case cannot reach the
// derivative either, even though it was produced FROM a document that
// (before redaction) only the source case's members could see — the
// derivative inherits the SAME case, and therefore the same access rule,
// never open access merely because it now exists.
func TestRedactDocument_DerivativeAccessIndependentlyControlled(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "redact-officerA12@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "redact-officerB12@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "redact-admin12@example.com", models.RoleAdmin)
	caseA := mustSeedCase(t, migrator, "REDACT-CASE-12A", officerA)

	pngBytes, _ := redactTestPNG(t)
	original, err := svc.UploadDocument(ctx, authUser(officerA, models.RolePolice), caseA, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	summary, err := svc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Protect witness identity",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.NoError(t, err)

	_, err = svc.DownloadDocument(ctx, authUser(officerB, models.RolePolice), summary.Document.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "an officer unrelated to the source case must not be able to reach the derivative either")
}
