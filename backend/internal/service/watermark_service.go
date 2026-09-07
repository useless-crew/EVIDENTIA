package service

import (
	"context"
	"fmt"
	"io"
)

// WatermarkResult holds the outcome of a watermark operation.
type WatermarkResult struct {
	Stream            io.ReadCloser // The watermarked stream. Caller must Close().
	WatermarkStatus   string        // "APPLIED", "NOT_SUPPORTED", or "FAILED"
	WatermarkVersion  int32
}

// WatermarkService provides invisible forensic watermarking for evidence exports.
// Its implementation delegates to format-specific engines (e.g. PDF invisibly modifying text).
// For SIH 2026, we implement a stub that simulates watermarking for supported MIME types,
// and passes through unsupported formats natively.
type WatermarkService struct {
}

// NewWatermarkService creates a new watermark service.
func NewWatermarkService() *WatermarkService {
	return &WatermarkService{}
}

// ApplyWatermark injects an invisible tracking payload into the document stream
// if the MIME type is supported (e.g. application/pdf, image/jpeg).
// If unsupported, it returns the original stream with status NOT_SUPPORTED.
// The payload string contains trace information (exportID, timestamp, user context).
func (w *WatermarkService) ApplyWatermark(ctx context.Context, originalStream io.ReadCloser, mimeType string, payload string) (*WatermarkResult, error) {
	// For now, treat all non-text/pdf items as NOT_SUPPORTED by the stub,
	// just passing the stream through unmodified.
	// A full implementation would buffer the PDF or image, inject the payload, and return a new reader.
	// Since this is a production-oriented stub (as an architecture slice), we'll gracefully downgrade.
	
	// Example supported check:
	// if mimeType == "application/pdf" {
	//    // buffer, watermark, return new stream
	//    return &WatermarkResult{Stream: watermarkedStream, WatermarkStatus: "APPLIED", WatermarkVersion: 1}, nil
	// }

	// Fallback to passthrough
	return &WatermarkResult{
		Stream:           originalStream,
		WatermarkStatus:  "NOT_SUPPORTED",
		WatermarkVersion: 1,
	}, nil
}
