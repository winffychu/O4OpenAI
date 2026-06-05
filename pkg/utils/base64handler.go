package utils

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Base64 Image Handler
//
// Problem: OpenAI clients send images as base64 data URIs:
//   data:image/png;base64,iVBORw0KGgo...
//
// But many providers (like Agnes AI) only accept image URLs.
//
// Solution: This handler converts base64 data URIs to temporary
// URLs that the provider can fetch.
//
// Lifecycle:
//   1. Client sends base64 → Gateway converts to temp URL
//   2. Provider request is made with temp URLs
//   3. Provider fetches temp images from our server
//   4. AFTER the provider request completes (success or failure),
//      we immediately delete the temp images
//   5. Safety net: periodic cleanup removes any stale images
//      (e.g., if the process crashed mid-request)
//
// Supported scenarios:
//   1. Chat with image_url (base64) - 多模态对话
//   2. Image edit with base64 image/mask - 图生图
//   3. Image-to-video with base64 - 图生视频
//   4. Multi-image input - 多图输入
//   5. First/last frame for video - 首尾帧生视频
// ============================================================

// TempImage represents a temporarily stored image
type TempImage struct {
	Data        []byte    // Raw image bytes
	ContentType string    // MIME type
	CreatedAt   time.Time // When it was stored
	ExpiresAt   time.Time // Safety net expiration
}

// RequestContext tracks temp images created during a single API request
type RequestContext struct {
	hashes []string // hashes of images created in this request
}

// Base64Handler manages temporary image storage and serving
type Base64Handler struct {
	mu       sync.RWMutex
	images   map[string]*TempImage // hash -> image
	baseURL  string                // Externally accessible URL
	ttl      time.Duration         // Safety net TTL
}

// NewBase64Handler creates a new handler
func NewBase64Handler(baseURL string, ttl time.Duration) *Base64Handler {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &Base64Handler{
		images:  make(map[string]*TempImage),
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     ttl,
	}
}

// IsDataURL checks if a string is a base64 data URI
func IsDataURL(s string) bool {
	return strings.HasPrefix(s, "data:")
}

// NewRequestContext creates a new context for tracking images in a single request.
// Usage:
//   ctx := handler.NewRequestContext()
//   url, _, _ := handler.ConvertDataURL(dataURI)        // stores image
//   resp, err := provider.Call(...)                      // provider fetches image
//   ctx.Cleanup(handler)                                  // delete image immediately
func NewRequestContext() *RequestContext {
	return &RequestContext{}
}

// ConvertDataURL converts a data URI to a temp URL that the provider can fetch.
// If the input is already a regular URL, it returns it unchanged.
// If a RequestContext is provided, the image hash is tracked for later cleanup.
// Returns: (url, wasConverted, error)
func (h *Base64Handler) ConvertDataURL(dataURI string, reqCtx ...*RequestContext) (string, bool, error) {
	if !IsDataURL(dataURI) {
		return dataURI, false, nil
	}

	// Parse the data URI
	contentType, data, err := parseDataURI(dataURI)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse data URI: %w", err)
	}

	// Generate a hash-based key
	hash := fmt.Sprintf("%x", sha256.Sum256(data))[:16]

	// Store the image with safety-net TTL
	h.mu.Lock()
	h.images[hash] = &TempImage{
		Data:        data,
		ContentType: contentType,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(h.ttl),
	}
	h.mu.Unlock()

	// Track in request context for cleanup after request completes
	if len(reqCtx) > 0 && reqCtx[0] != nil {
		reqCtx[0].hashes = append(reqCtx[0].hashes, hash)
	}

	// Generate the temp URL
	tempURL := fmt.Sprintf("%s/_temp/images/%s", h.baseURL, hash)
	return tempURL, true, nil
}

// CleanupRequest removes all temp images associated with a request context.
// Call this AFTER the provider request completes (success or failure).
// This is the primary cleanup mechanism.
func (h *Base64Handler) CleanupRequest(ctx *RequestContext) {
	if ctx == nil || len(ctx.hashes) == 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, hash := range ctx.hashes {
		delete(h.images, hash)
	}
	ctx.hashes = nil
}

// CleanupStale removes expired images (safety net for crashes/interruptions)
func (h *Base64Handler) CleanupStale() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	removed := 0
	for key, img := range h.images {
		if now.After(img.ExpiresAt) {
			delete(h.images, key)
			removed++
		}
	}
	return removed
}

// StartCleanupRoutine starts a periodic safety-net cleanup.
// This only catches images that weren't cleaned up by CleanupRequest
// (e.g., due to crashes or unexpected interruptions).
func (h *Base64Handler) StartCleanupRoutine(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			h.CleanupStale()
		}
	}()
}

// GetImage retrieves a stored image by its hash key
func (h *Base64Handler) GetImage(hash string) (*TempImage, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	img, ok := h.images[hash]
	if !ok {
		return nil, false
	}

	// Check safety-net expiration
	if time.Now().After(img.ExpiresAt) {
		return nil, false
	}

	return img, true
}

// ServeHTTP implements http.Handler for serving temp images
func (h *Base64Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "invalid temp image URL", http.StatusBadRequest)
		return
	}
	hash := parts[len(parts)-1]

	img, ok := h.GetImage(hash)
	if !ok {
		http.Error(w, "image not found or expired", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Cache-Control", "no-store") // Don't cache - image will be deleted
	w.Write(img.Data)
}

// Stats returns current stats about stored images
func (h *Base64Handler) Stats() (count int, totalBytes int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, img := range h.images {
		count++
		totalBytes += int64(len(img.Data))
	}
	return
}

// parseDataURI parses a data URI into its components
func parseDataURI(dataURI string) (contentType string, data []byte, err error) {
	if !strings.HasPrefix(dataURI, "data:") {
		return "", nil, fmt.Errorf("not a data URI")
	}

	rest := dataURI[5:]
	commaIdx := strings.Index(rest, ",")
	if commaIdx == -1 {
		return "", nil, fmt.Errorf("invalid data URI: missing comma")
	}

	meta := rest[:commaIdx]
	encodedData := rest[commaIdx+1:]

	isBase64 := false
	contentType = "application/octet-stream"

	if meta != "" {
		parts := strings.Split(meta, ";")
		if parts[0] != "" {
			contentType = parts[0]
		}
		for _, p := range parts[1:] {
			if p == "base64" {
				isBase64 = true
			}
		}
	}

	if isBase64 {
		data, err = base64.StdEncoding.DecodeString(encodedData)
		if err != nil {
			data, err = base64.URLEncoding.DecodeString(encodedData)
			if err != nil {
				return "", nil, fmt.Errorf("failed to decode base64: %w", err)
			}
		}
	} else {
		data = []byte(encodedData)
	}

	if contentType == "application/octet-stream" || contentType == "text/plain" {
		contentType = inferContentType(data)
	}

	return contentType, data, nil
}

// inferContentType determines the image type from magic bytes
func inferContentType(data []byte) string {
	if len(data) < 4 {
		return "application/octet-stream"
	}
	switch {
	case data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return "image/png"
	case data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return "image/gif"
	case data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46:
		return "image/webp"
	case len(data) > 8 && string(data[4:11]) == "ftyp":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
