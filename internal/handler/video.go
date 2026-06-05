package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// VideoHandler handles video-related requests
type VideoHandler struct {
	registry       *provider.Registry
	logger         *zap.Logger
	base64Handler  *utils.Base64Handler
	forcedProvider string
}

// NewVideoHandler creates a new video handler
func NewVideoHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *VideoHandler {
	return &VideoHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

// HandleGenerate handles POST /v1/videos/generations
func (h *VideoHandler) HandleGenerate(c *gin.Context) {
	var req model.VideoGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	h.logger.Info("Video generation request", zap.String("model", req.Model))

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "Model is required for video generation",
				Type:    "invalid_request_error",
				Code:    "missing_model",
				Param:   "model",
			},
		})
		return
	}

	var p model.Provider
	var err error
	if h.forcedProvider != "" {
		p, err = h.registry.GetProvider(h.forcedProvider)
	} else {
		p, err = h.registry.GetProviderForModel(req.Model)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found for video generation", req.Model),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
			},
		})
		return
	}

	if !p.SupportsVideoGeneration() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support video generation", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	resp, err := p.VideoGeneration(ctxWithKey(c), &req)
	if err != nil {
		h.logger.Error("Video generation failed", zap.Error(err))
		respondProviderError(c, "Video generation", err)
		return
	}

	if resp.Status == "processing" {
		c.JSON(http.StatusAccepted, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleRetrieve handles GET /v1/videos/:id
func (h *VideoHandler) HandleRetrieve(c *gin.Context) {
	videoID := c.Param("id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "Video ID is required",
				Type:    "invalid_request_error",
				Code:    "missing_video_id",
			},
		})
		return
	}

	ctx := ctxWithKey(c)

	// Try the forced provider first, then iterate over all providers.
	// A 404 from the upstream means the task ID isn't there — try the next one.
	// Any other error (auth, network, etc.) is reported to the caller.
	providers := h.providersToTry()

	var lastUpstreamErr error
	for _, p := range providers {
		if !p.SupportsVideoGeneration() {
			continue
		}
		resp, err := p.VideoRetrieve(ctx, videoID)
		if err == nil {
			c.JSON(http.StatusOK, resp)
			return
		}
		// If the upstream says "not found", keep trying; otherwise stop.
		if isUpstreamNotFound(err) {
			lastUpstreamErr = err
			continue
		}
		respondProviderError(c, "Video retrieve", err)
		return
	}

	if lastUpstreamErr != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Video %q not found", videoID),
				Type:    "invalid_request_error",
				Code:    "video_not_found",
			},
		})
		return
	}
	c.JSON(http.StatusNotFound, model.ErrorResponse{
		Error: model.ErrorDetail{
			Message: fmt.Sprintf("Video %q not found", videoID),
			Type:    "invalid_request_error",
			Code:    "video_not_found",
		},
	})
}

// providersToTry returns the provider list to attempt, starting with
// the forced provider if one is configured.
func (h *VideoHandler) providersToTry() []model.Provider {
	if h.forcedProvider != "" {
		if p, err := h.registry.GetProvider(h.forcedProvider); err == nil {
			return []model.Provider{p}
		}
		return nil
	}
	all := h.registry.GetAllProviders()
	out := make([]model.Provider, 0, len(all))
	for _, name := range all {
		if p, err := h.registry.GetProvider(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// isUpstreamNotFound reports whether the upstream returned 404 for the request.
func isUpstreamNotFound(err error) bool {
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusNotFound
	}
	return false
}
