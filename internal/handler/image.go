package handler

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// Suppress unused import

// ImageHandler handles image-related requests
type ImageHandler struct {
	registry        *provider.Registry
	logger          *zap.Logger
	base64Handler   *utils.Base64Handler
	forcedProvider  string
}

// NewImageHandler creates a new image handler
func NewImageHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *ImageHandler {
	return &ImageHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

// resolveProvider finds the provider by forced name or model name
func (h *ImageHandler) resolveProvider(modelName string) (model.Provider, error) {
	if h.forcedProvider != "" {
		return h.registry.GetProvider(h.forcedProvider)
	}
	if modelName != "" {
		return h.registry.GetProviderForModel(modelName)
	}
	return h.registry.GetProviderForModel("dall-e-2") // fallback default
}

// HandleGenerate handles POST /v1/images/generations
func (h *ImageHandler) HandleGenerate(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")

	var req model.ImageGenerationRequest
	var err error

	if strings.HasPrefix(contentType, "multipart/form-data") {
		err = h.parseMultipartGenerate(c, &req)
	} else {
		err = c.ShouldBindJSON(&req)
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	h.logger.Info("Image generation request",
		zap.String("model", req.Model),
		zap.String("size", req.Size),
		zap.String("response_format", req.ResponseFormat),
	)

	// Determine which model to use
	modelName := req.Model
	if modelName == "" {
		modelName = "dall-e-2" // OpenAI default
	}

	p, err := h.resolveProvider(modelName)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found for image generation", modelName),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
			},
		})
		return
	}

	if !p.SupportsImageGeneration() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support image generation", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	resp, err := p.ImageGeneration(ctxWithKey(c), &req)
	if err != nil {
		h.logger.Error("Image generation failed", zap.Error(err))
		respondProviderError(c, "Image generation", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleEdit handles POST /v1/images/edits
func (h *ImageHandler) HandleEdit(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")

	var req model.ImageEditRequest
	var err error

	if strings.HasPrefix(contentType, "multipart/form-data") {
		err = h.parseMultipartEdit(c, &req)
	} else {
		err = c.ShouldBindJSON(&req)
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	modelName := req.Model
	if modelName == "" {
		modelName = "dall-e-2"
	}

	p, err := h.resolveProvider(modelName)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found for image editing", modelName),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
			},
		})
		return
	}

	if !p.SupportsImageEdit() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support image editing", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	resp, err := p.ImageEdit(ctxWithKey(c), &req)
	if err != nil {
		h.logger.Error("Image edit failed", zap.Error(err))
		respondProviderError(c, "Image edit", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleVariation handles POST /v1/images/variations
func (h *ImageHandler) HandleVariation(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")

	var req model.ImageVariationRequest
	var err error

	if strings.HasPrefix(contentType, "multipart/form-data") {
		err = h.parseMultipartVariation(c, &req)
	} else {
		err = c.ShouldBindJSON(&req)
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	modelName := req.Model
	if modelName == "" {
		modelName = "dall-e-2"
	}

	p, err := h.resolveProvider(modelName)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found for image variations", modelName),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
			},
		})
		return
	}

	if !p.SupportsImageVariation() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support image variations", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	resp, err := p.ImageVariation(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Image variation failed", zap.Error(err))
		respondProviderError(c, "Image variation", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// parseMultipartGenerate parses multipart form data for image generation
func (h *ImageHandler) parseMultipartGenerate(c *gin.Context, req *model.ImageGenerationRequest) error {
	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	// Parse simple fields
	if values := form.Value["model"]; len(values) > 0 {
		req.Model = values[0]
	}
	if values := form.Value["prompt"]; len(values) > 0 {
		req.Prompt = values[0]
	}
	if values := form.Value["size"]; len(values) > 0 {
		req.Size = values[0]
	}
	if values := form.Value["quality"]; len(values) > 0 {
		req.Quality = values[0]
	}
	if values := form.Value["response_format"]; len(values) > 0 {
		req.ResponseFormat = values[0]
	}
	if values := form.Value["style"]; len(values) > 0 {
		req.Style = values[0]
	}
	if values := form.Value["user"]; len(values) > 0 {
		req.User = values[0]
	}
	if values := form.Value["n"]; len(values) > 0 {
		var n int
		fmt.Sscanf(values[0], "%d", &n)
		req.N = &n
	}

	return nil
}

// parseMultipartEdit parses multipart form data for image editing
func (h *ImageHandler) parseMultipartEdit(c *gin.Context, req *model.ImageEditRequest) error {
	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	if values := form.Value["model"]; len(values) > 0 {
		req.Model = values[0]
	}
	if values := form.Value["prompt"]; len(values) > 0 {
		req.Prompt = values[0]
	}
	if values := form.Value["size"]; len(values) > 0 {
		req.Size = values[0]
	}
	if values := form.Value["response_format"]; len(values) > 0 {
		req.ResponseFormat = values[0]
	}
	if values := form.Value["n"]; len(values) > 0 {
		var n int
		fmt.Sscanf(values[0], "%d", &n)
		req.N = &n
	}

	// Parse file uploads - convert to base64
	if files := form.File["image"]; len(files) > 0 {
		file, err := files[0].Open()
		if err != nil {
			return fmt.Errorf("failed to open image file: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("failed to read image file: %w", err)
		}
		req.Image = encodeToBase64String(data)
	}

	if files := form.File["mask"]; len(files) > 0 {
		file, err := files[0].Open()
		if err != nil {
			return fmt.Errorf("failed to open mask file: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("failed to read mask file: %w", err)
		}
		req.Mask = encodeToBase64String(data)
	}

	return nil
}

// parseMultipartVariation parses multipart form data for image variations
func (h *ImageHandler) parseMultipartVariation(c *gin.Context, req *model.ImageVariationRequest) error {
	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	if values := form.Value["model"]; len(values) > 0 {
		req.Model = values[0]
	}
	if values := form.Value["size"]; len(values) > 0 {
		req.Size = values[0]
	}
	if values := form.Value["response_format"]; len(values) > 0 {
		req.ResponseFormat = values[0]
	}
	if values := form.Value["n"]; len(values) > 0 {
		var n int
		fmt.Sscanf(values[0], "%d", &n)
		req.N = &n
	}

	if files := form.File["image"]; len(files) > 0 {
		file, err := files[0].Open()
		if err != nil {
			return fmt.Errorf("failed to open image file: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("failed to read image file: %w", err)
		}
		req.Image = encodeToBase64String(data)
	}

	return nil
}

// encodeToBase64String encodes bytes to base64 string
func encodeToBase64String(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

