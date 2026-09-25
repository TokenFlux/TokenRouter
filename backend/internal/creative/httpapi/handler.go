package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

// 创作台 multipart 单字段大小上限：配置值通过 limit+1 读取检测，避免静默截断。
const creativeMaxUploadPartSize = 40 << 20

// multipart 请求体除图片外还包含字段与边界，预留少量空间后再套用总输入上限。
const creativeMultipartOverheadBytes = 1 << 20

// CreativeHandler 处理创作台用户侧请求。
type CreativeHandler struct {
	enter   func() (func(), error)
	service CreativeUseCases
}

// NewCreativeHandler 创建创作台 handler。
func NewCreativeHandler(service CreativeUseCases) *CreativeHandler {
	return &CreativeHandler{service: service}
}

// creativeRunScopeFromRequest 解析创作台浏览器工作区并绑定当前用户身份。
func creativeRunScopeFromRequest(c *gin.Context, userID int64) (service.CreativeRunScope, error) {
	workspaceID, err := service.NormalizeCreativeWorkspaceID(c.GetHeader(service.CreativeWorkspaceHeader))
	if err != nil {
		return service.CreativeRunScope{}, err
	}
	return service.CreativeRunScope{UserID: userID, WorkspaceID: workspaceID}, nil
}

// ListModels 返回当前用户可用的分组 + 图片模型组合。
// GET /api/v1/creative/models
func (h *CreativeHandler) ListModels(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	got, err := h.service.ListModels(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 面板 envelope 的 data 直接放数组，避免再包一层 {data: [...]}。
	response.Success(c, got.Data)
}

// ListCapabilities 返回创作台输入限制与允许的图片 MIME 类型。
// GET /api/v1/creative/capabilities
func (h *CreativeHandler) ListCapabilities(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	if _, ok := middleware2.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	response.Success(c, h.service.GetCapabilities(c.Request.Context()))
}

// creativeCreateRunRequest 是创建任务 multipart 报文的解析结果。
type creativeCreateRunRequest struct {
	GroupID       int64
	Model         string
	Operation     string
	Prompt        string
	SourceImages  []service.CreativeInputImage
	Mask          *service.CreativeInputImage
	ImageSize     string
	AspectRatio   string
	Quality       string
	Background    string
	ThinkingLevel string
}

// CreateRun 解析 multipart/form-data 并创建创作台任务。
// POST /api/v1/creative/runs
func (h *CreativeHandler) CreateRun(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	partLimit := int64(creativeMaxUploadPartSize)
	totalInputLimit := int64(64 << 20)
	if h.service != nil && h.service.MaxAssetBytes() > 0 {
		partLimit = h.service.MaxAssetBytes()
	}
	if h.service != nil && h.service.MaxTotalInputBytes() > 0 {
		totalInputLimit = h.service.MaxTotalInputBytes()
	}
	// 先限制整个请求体，避免解析器在服务层总量校验前累积任意多的文件。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, totalInputLimit+creativeMultipartOverheadBytes)
	req, err := parseCreativeCreateRunMultipart(c, partLimit, totalInputLimit)
	if err != nil {
		if errors.Is(err, service.ErrCreativeAssetTooLarge) || errors.Is(err, service.ErrCreativeInputTooLarge) {
			response.ErrorFrom(c, err)
			return
		}
		response.ErrorFrom(c, service.ErrCreativeInvalidParams)
		return
	}
	got, err := h.service.CreateRun(c.Request.Context(), scope, service.CreateCreativeRunParamsPublic{
		GroupID:       req.GroupID,
		Model:         req.Model,
		Operation:     req.Operation,
		Prompt:        req.Prompt,
		SourceImages:  req.SourceImages,
		Mask:          req.Mask,
		ImageSize:     req.ImageSize,
		AspectRatio:   req.AspectRatio,
		Quality:       req.Quality,
		Background:    req.Background,
		ThinkingLevel: req.ThinkingLevel,
	}, c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, got)
}

// parseCreativeCreateRunMultipart 手工解析 multipart 表单：
// 字段 group_id/model/operation/prompt/image_size/aspect_ratio/quality/background/
// thinking_level，
// 文件字段 source_images（多文件）与 mask（单文件）。只接受上传文件，不接受远程 URL。
func parseCreativeCreateRunMultipart(c *gin.Context, partLimit, totalInputLimit int64) (*creativeCreateRunRequest, error) {
	contentType := c.GetHeader("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, http.ErrMissingBoundary
	}
	req := &creativeCreateRunRequest{}
	if totalInputLimit <= 0 {
		totalInputLimit = 64 << 20
	}
	var totalFileBytes int64
	reader := multipart.NewReader(c.Request.Body, boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		fileName := strings.TrimSpace(part.FileName())
		if fileName != "" {
			if partLimit <= 0 {
				partLimit = creativeMaxUploadPartSize
			}
			remaining := totalInputLimit - totalFileBytes
			if remaining <= 0 {
				_ = part.Close()
				return nil, service.ErrCreativeInputTooLarge
			}
			readLimit := partLimit
			if remaining < readLimit {
				readLimit = remaining
			}
			data, readErr := io.ReadAll(io.LimitReader(part, readLimit+1))
			_ = part.Close()
			if readErr != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(readErr, &maxBytesErr) {
					return nil, service.ErrCreativeInputTooLarge
				}
				return nil, readErr
			}
			if int64(len(data)) > readLimit {
				if readLimit < partLimit {
					return nil, service.ErrCreativeInputTooLarge
				}
				return nil, service.ErrCreativeAssetTooLarge
			}
			totalFileBytes += int64(len(data))
			partMIME := normalizeCreativeUploadMime(strings.TrimSpace(part.Header.Get("Content-Type")), data)
			switch {
			case name == "mask":
				if len(data) > 0 {
					req.Mask = &service.CreativeInputImage{Bytes: data, Mime: partMIME}
				}
			case name == "source_images" || name == "source_images[]" || strings.HasPrefix(name, "source_images["):
				if len(data) > 0 {
					req.SourceImages = append(req.SourceImages, service.CreativeInputImage{Bytes: data, Mime: partMIME})
				}
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
		_ = part.Close()
		if readErr != nil {
			return nil, readErr
		}
		value := strings.TrimSpace(string(data))
		switch name {
		case "group_id":
			groupID, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || groupID <= 0 {
				return nil, service.ErrCreativeInvalidParams
			}
			req.GroupID = groupID
		case "model":
			req.Model = value
		case "operation":
			req.Operation = strings.ToLower(value)
		case "prompt":
			req.Prompt = value
		case "image_size":
			req.ImageSize = value
		case "aspect_ratio":
			req.AspectRatio = value
		case "quality":
			req.Quality = value
		case "output_format", "output_compression", "response_mime_type":
			// 创作台统一输出 PNG，旧格式参数不再兼容。
			return nil, service.ErrCreativeInvalidParams
		case "background":
			req.Background = strings.ToLower(value)
		case "thinking_level":
			req.ThinkingLevel = strings.ToLower(value)
		}
	}
	if req.GroupID <= 0 {
		return nil, service.ErrCreativeInvalidParams
	}
	return req, nil
}

// normalizeCreativeUploadMime 归一化上传文件 MIME；缺失或非法时按字节魔数嗅探。
func normalizeCreativeUploadMime(headerMIME string, data []byte) string {
	switch strings.ToLower(strings.TrimSpace(headerMIME)) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/webp":
		return "image/webp"
	}
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return strings.ToLower(strings.TrimSpace(headerMIME))
}

// ListRuns 返回当前用户的任务列表。
// GET /api/v1/creative/runs
func (h *CreativeHandler) ListRuns(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	got, err := h.service.ListRuns(c.Request.Context(), scope, service.CreativeRunFilter{
		Status: strings.TrimSpace(c.Query("status")),
		Limit:  limit,
		Offset: 0,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 面板 envelope 的 data 直接放数组，前端按数组或 {items,total} 宽容解析。
	response.Success(c, got.Data)
}

// ListActiveRuns 返回所有尚未进入可交付终态的任务，使用不透明游标分页。
// GET /api/v1/creative/runs/active?limit=100&cursor=<opaque>
func (h *CreativeHandler) ListActiveRuns(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset := 0
	if rawCursor := strings.TrimSpace(c.Query("cursor")); rawCursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(rawCursor)
		if decodeErr != nil {
			response.ErrorFrom(c, service.ErrCreativeInvalidParams)
			return
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil || offset < 0 {
			response.ErrorFrom(c, service.ErrCreativeInvalidParams)
			return
		}
	}
	got, err := h.service.ListRuns(c.Request.Context(), scope, service.CreativeRunFilter{
		Status: "active",
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	nextCursor := ""
	if got.HasMore {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(got.Data))))
	}
	response.Success(c, gin.H{
		"items":       got.Data,
		"next_cursor": nextCursor,
		"has_more":    got.HasMore,
	})
}

// GetRun 返回单个任务详情。
// GET /api/v1/creative/runs/:id
func (h *CreativeHandler) GetRun(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	got, err := h.service.GetRun(c.Request.Context(), scope, c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, got)
}

// GetOutputContent 返回临时输出图片字节；过期返回 410 风格错误。
// GET /api/v1/creative/runs/:id/outputs/:index/content
func (h *CreativeHandler) GetOutputContent(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	outputIndex, err := strconv.Atoi(c.Param("index"))
	if err != nil || outputIndex < 0 {
		response.ErrorFrom(c, service.ErrCreativeOutputNotFound)
		return
	}
	content, err := h.service.GetOutputContent(c.Request.Context(), scope, c.Param("id"), outputIndex)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, content.ContentType, content.Content)
}

// AckOutput 客户端确认接收后删除临时输出。
// POST /api/v1/creative/runs/:id/outputs/:index/ack
func (h *CreativeHandler) AckOutput(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	outputIndex, err := strconv.Atoi(c.Param("index"))
	if err != nil || outputIndex < 0 {
		response.ErrorFrom(c, service.ErrCreativeOutputNotFound)
		return
	}
	if err := h.service.AckOutput(c.Request.Context(), scope, c.Param("id"), outputIndex); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"acked": true})
}

// CreativeUseCases 只暴露 HTTP 消费的任务契约，不依赖旧聚合服务。
type CreativeUseCases interface {
	ListModels(context.Context, int64) (*service.CreativeModelsResponse, error)
	GetCapabilities(context.Context) *service.CreativeCapabilitiesResponse
	MaxAssetBytes() int64
	MaxTotalInputBytes() int64
	CreateRun(context.Context, service.CreativeRunScope, service.CreateCreativeRunParamsPublic, string) (*service.CreativeRunPublic, error)
	ListRuns(context.Context, service.CreativeRunScope, service.CreativeRunFilter) (*service.CreativeListRunsResponse, error)
	GetRun(context.Context, service.CreativeRunScope, string) (*service.CreativeRunPublic, error)
	GetOutputContent(context.Context, service.CreativeRunScope, string, int) (*service.CreativeOutputContent, error)
	AckOutput(context.Context, service.CreativeRunScope, string, int) error
}

// BindActivity 在构造阶段绑定任务入口关闭屏障，释放覆盖完整 HTTP 流式输出。
func (h *CreativeHandler) BindActivity(enter func() (func(), error)) { h.enter = enter }
