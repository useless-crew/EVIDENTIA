package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// wmMagic is the 8-byte sentinel that opens and closes every embedded
// watermark block. High-bit bytes chosen to be extremely unlikely to appear
// naturally at the tail of any PDF/JPEG/PNG stream.
var wmMagic = []byte{0xE7, 0x1D, 0xA0, 0xF0, 0x1C, 0xE7, 0x1D, 0xA0}

const wmVersion = 1

// WatermarkPayload is the structured forensic identity record embedded
// invisibly into every exported evidence file. All fields are exported so
// json.Marshal/Unmarshal works correctly across encrypt/decrypt cycles.
type WatermarkPayload struct {
	Version     int    `json:"v"`
	ExportID    string `json:"eid"`
	DocumentID  string `json:"did"`
	UserID      string `json:"uid"`
	ExportedAt  string `json:"at"`
	ClientIP    string `json:"ip"`
	Fingerprint string `json:"fp"`
}

// WatermarkResult holds the outcome of a watermark operation.
type WatermarkResult struct {
	Stream           io.ReadCloser
	WatermarkStatus  string // "APPLIED", "NOT_SUPPORTED", "FAILED"
	WatermarkVersion int32
}

// WatermarkService provides invisible forensic watermarking for evidence
// exports. The watermark block is appended after the file's natural end
// marker (JPEG EOI, PNG IEND, PDF %%EOF) so renderers never see it but
// the raw bytes are always accessible for forensic extraction.
type WatermarkService struct {
	key []byte // 32-byte AES-256 key derived from the server signing key
}

// NewWatermarkService creates a WatermarkService whose AES key is derived
// from signingKey with SHA-256, so any key length is accepted and only
// this server can ever encrypt or decrypt watermark payloads.
func NewWatermarkService(signingKey string) *WatermarkService {
	h := sha256.Sum256([]byte("evidentia:watermark:v1:" + signingKey))
	return &WatermarkService{key: h[:]}
}

// ApplyWatermark embeds an AES-256-GCM encrypted forensic payload into
// originalStream. The payload is invisible during normal rendering of the
// document but is fully recoverable by ExtractWatermark. On any per-format
// embedding failure it falls back to passing the original stream through
// unmodified and returns FAILED status (never an error — the caller always
// gets a usable stream back).
func (w *WatermarkService) ApplyWatermark(
	ctx context.Context,
	originalStream io.ReadCloser,
	mimeType string,
	payload WatermarkPayload,
) (*WatermarkResult, error) {
	data, err := io.ReadAll(originalStream)
	originalStream.Close()
	if err != nil {
		return nil, fmt.Errorf("read source for watermarking: %w", err)
	}

	encrypted, err := w.encrypt(payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt watermark payload: %w", err)
	}
	wmBlock := buildBlock(encrypted)

	var out []byte
	switch mimeType {
	case "image/jpeg", "image/jpg":
		out, err = embedAfterJPEGEOI(data, wmBlock)
	case "image/png":
		out, err = embedAfterPNGIEND(data, wmBlock)
	case "application/pdf":
		out, err = embedAfterPDFEOF(data, wmBlock)
	default:
		out, err = embedGeneric(data, wmBlock)
	}

	if err != nil {
		// Graceful fallback: return original bytes, mark as FAILED
		return &WatermarkResult{
			Stream:           io.NopCloser(bytes.NewReader(data)),
			WatermarkStatus:  "FAILED",
			WatermarkVersion: wmVersion,
		}, nil
	}

	return &WatermarkResult{
		Stream:           io.NopCloser(bytes.NewReader(out)),
		WatermarkStatus:  "APPLIED",
		WatermarkVersion: wmVersion,
	}, nil
}

// ExtractWatermark scans data for the embedded block, decrypts it, and
// returns the structured forensic payload. Returns a descriptive error if
// no valid block is found or the data has been tampered with.
func (w *WatermarkService) ExtractWatermark(data []byte) (*WatermarkPayload, error) {
	enc := extractBlock(data)
	if enc == nil {
		return nil, errors.New("no forensic watermark found in this file")
	}
	return w.decrypt(enc)
}

// ---- encryption / decryption ----------------------------------------

func (w *WatermarkService) encrypt(payload WatermarkPayload) ([]byte, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(w.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	// Seal appends ciphertext+tag to nonce so the nonce is self-contained.
	ciphertext := gcm.Seal(nonce, nonce, plain, nil)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(ciphertext)))
	base64.StdEncoding.Encode(encoded, ciphertext)
	return encoded, nil
}

func (w *WatermarkService) decrypt(enc []byte) (*WatermarkPayload, error) {
	ciphertext := make([]byte, base64.StdEncoding.DecodedLen(len(enc)))
	n, err := base64.StdEncoding.Decode(ciphertext, enc)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	ciphertext = ciphertext[:n]

	block, err := aes.NewCipher(w.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short — file may be corrupted or truncated")
	}
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("watermark decryption failed — wrong server key or watermark has been tampered with")
	}
	var p WatermarkPayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return &p, nil
}

// ---- watermark block format -----------------------------------------
//
// [ magic(8) ][ uint32BE payload_len(4) ][ encrypted_payload(N) ][ magic(8) ]
//
// The repeated magic sentinels allow reliable scanning even in dense binary
// files; the length field prevents ambiguous reads.

func buildBlock(enc []byte) []byte {
	buf := make([]byte, len(wmMagic)+4+len(enc)+len(wmMagic))
	off := 0
	copy(buf[off:], wmMagic)
	off += len(wmMagic)
	binary.BigEndian.PutUint32(buf[off:], uint32(len(enc)))
	off += 4
	copy(buf[off:], enc)
	off += len(enc)
	copy(buf[off:], wmMagic)
	return buf
}

func extractBlock(data []byte) []byte {
	start := bytes.Index(data, wmMagic)
	if start < 0 {
		return nil
	}
	rest := data[start+len(wmMagic):]
	if len(rest) < 4 {
		return nil
	}
	n := int(binary.BigEndian.Uint32(rest[:4]))
	if len(rest) < 4+n+len(wmMagic) {
		return nil
	}
	enc := rest[4 : 4+n]
	// Verify closing magic to rule out false positives
	if !bytes.Equal(rest[4+n:4+n+len(wmMagic)], wmMagic) {
		return nil
	}
	return enc
}

// ---- per-format embedding -------------------------------------------

// embedAfterJPEGEOI appends the watermark block after the JPEG EOI marker
// (0xFF 0xD9). Standard JPEG decoders stop at EOI; the trailing bytes are
// completely invisible during normal display or printing.
func embedAfterJPEGEOI(data, wmBlock []byte) ([]byte, error) {
	eoi := []byte{0xFF, 0xD9}
	idx := bytes.LastIndex(data, eoi)
	if idx < 0 {
		return nil, errors.New("JPEG EOI marker (0xFF 0xD9) not found")
	}
	out := make([]byte, 0, len(data)+len(wmBlock))
	out = append(out, data[:idx+2]...) // include EOI
	out = append(out, wmBlock...)
	return out, nil
}

// embedAfterPNGIEND appends the watermark block after the PNG IEND chunk.
// PNG parsers stop at IEND; trailing bytes are completely ignored.
func embedAfterPNGIEND(data, wmBlock []byte) ([]byte, error) {
	iend := []byte("IEND")
	idx := bytes.LastIndex(data, iend)
	if idx < 0 {
		return nil, errors.New("PNG IEND chunk not found")
	}
	// IEND chunk layout: 4-byte len + 4-byte "IEND" + 4-byte CRC
	// idx points at "IEND", so end = idx + 4 (IEND) + 4 (CRC)
	end := idx + 8
	if end > len(data) {
		end = len(data)
	}
	out := make([]byte, 0, end+len(wmBlock))
	out = append(out, data[:end]...)
	out = append(out, wmBlock...)
	return out, nil
}

// embedAfterPDFEOF appends the watermark block after the last PDF %%EOF
// marker. PDF readers stop at %%EOF; trailing bytes are ignored by all
// standard renderers and do not affect any digital signatures over the PDF body.
func embedAfterPDFEOF(data, wmBlock []byte) ([]byte, error) {
	eof := []byte("%%EOF")
	idx := bytes.LastIndex(data, eof)
	if idx < 0 {
		return nil, errors.New("PDF %%EOF marker not found")
	}
	end := idx + len(eof)
	out := make([]byte, 0, end+1+len(wmBlock))
	out = append(out, data[:end]...)
	out = append(out, '\n') // newline separator for cleanliness
	out = append(out, wmBlock...)
	return out, nil
}

// embedGeneric appends the watermark block at the end of the file.
// Used for file types with no defined end-marker convention.
func embedGeneric(data, wmBlock []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)+len(wmBlock))
	out = append(out, data...)
	out = append(out, wmBlock...)
	return out, nil
}
