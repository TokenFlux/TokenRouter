// Package apperror provides application error types and helpers.
// nolint:mnd
package apperror

// BadRequest new BadRequest error that is mapped to a 400 response.
func BadRequest(reason, message string) *ApplicationError {
	return New(CategoryBadRequest, reason, message)
}

// IsBadRequest determines if err is an error which indicates a BadRequest error.
// It supports wrapped errors.
func IsBadRequest(err error) bool {
	return CategoryOf(err) == CategoryBadRequest
}

// TooManyRequests new TooManyRequests error that is mapped to a 429 response.
func TooManyRequests(reason, message string) *ApplicationError {
	return New(CategoryTooManyRequests, reason, message)
}

// IsTooManyRequests determines if err is an error which indicates a TooManyRequests error.
// It supports wrapped errors.
func IsTooManyRequests(err error) bool {
	return CategoryOf(err) == CategoryTooManyRequests
}

// Unauthorized new Unauthorized error that is mapped to a 401 response.
func Unauthorized(reason, message string) *ApplicationError {
	return New(CategoryUnauthorized, reason, message)
}

// IsUnauthorized determines if err is an error which indicates an Unauthorized error.
// It supports wrapped errors.
func IsUnauthorized(err error) bool {
	return CategoryOf(err) == CategoryUnauthorized
}

// Forbidden new Forbidden error that is mapped to a 403 response.
func Forbidden(reason, message string) *ApplicationError {
	return New(CategoryForbidden, reason, message)
}

// IsForbidden determines if err is an error which indicates a Forbidden error.
// It supports wrapped errors.
func IsForbidden(err error) bool {
	return CategoryOf(err) == CategoryForbidden
}

// NotFound new NotFound error that is mapped to a 404 response.
func NotFound(reason, message string) *ApplicationError {
	return New(CategoryNotFound, reason, message)
}

// IsNotFound determines if err is an error which indicates an NotFound error.
// It supports wrapped errors.
func IsNotFound(err error) bool {
	return CategoryOf(err) == CategoryNotFound
}

// Conflict new Conflict error that is mapped to a 409 response.
func Conflict(reason, message string) *ApplicationError {
	return New(CategoryConflict, reason, message)
}

// IsConflict determines if err is an error which indicates a Conflict error.
// It supports wrapped errors.
func IsConflict(err error) bool {
	return CategoryOf(err) == CategoryConflict
}

// InternalServer new InternalServer error that is mapped to a 500 response.
func InternalServer(reason, message string) *ApplicationError {
	return New(CategoryInternalServer, reason, message)
}

// IsInternalServer determines if err is an error which indicates an Internal error.
// It supports wrapped errors.
func IsInternalServer(err error) bool {
	return CategoryOf(err) == CategoryInternalServer
}

// ServiceUnavailable new ServiceUnavailable error that is mapped to an HTTP 503 response.
func ServiceUnavailable(reason, message string) *ApplicationError {
	return New(CategoryServiceUnavailable, reason, message)
}

// IsServiceUnavailable determines if err is an error which indicates an Unavailable error.
// It supports wrapped errors.
func IsServiceUnavailable(err error) bool {
	return CategoryOf(err) == CategoryServiceUnavailable
}

// GatewayTimeout new GatewayTimeout error that is mapped to an HTTP 504 response.
func GatewayTimeout(reason, message string) *ApplicationError {
	return New(CategoryGatewayTimeout, reason, message)
}

// IsGatewayTimeout determines if err is an error which indicates a GatewayTimeout error.
// It supports wrapped errors.
func IsGatewayTimeout(err error) bool {
	return CategoryOf(err) == CategoryGatewayTimeout
}

// ClientClosed new ClientClosed error that is mapped to an HTTP 499 response.
func ClientClosed(reason, message string) *ApplicationError {
	return New(CategoryClientClosed, reason, message)
}

// IsClientClosed determines if err is an error which indicates a IsClientClosed error.
// It supports wrapped errors.
func IsClientClosed(err error) bool {
	return CategoryOf(err) == CategoryClientClosed
}

// Category 为应用错误类别；数值在兼容期保留旧错误身份，不依赖 HTTP 包。
type Category int32

const (
	// CategoryOK 保留对应错误类别的稳定标识。
	CategoryOK Category = 200
	// CategoryBadRequest 保留对应错误类别的稳定标识。
	CategoryBadRequest Category = 400
	// CategoryUnauthorized 保留对应错误类别的稳定标识。
	CategoryUnauthorized Category = 401
	// CategoryForbidden 保留对应错误类别的稳定标识。
	CategoryForbidden Category = 403
	// CategoryNotFound 保留对应错误类别的稳定标识。
	CategoryNotFound Category = 404
	// CategoryConflict 保留对应错误类别的稳定标识。
	CategoryConflict Category = 409
	// CategoryTooManyRequests 保留对应错误类别的稳定标识。
	CategoryTooManyRequests Category = 429
	// CategoryClientClosed 保留对应错误类别的稳定标识。
	CategoryClientClosed Category = 499
	// CategoryInternalServer 保留对应错误类别的稳定标识。
	CategoryInternalServer Category = 500
	// CategoryBadGateway 保留对应错误类别的稳定标识。
	CategoryBadGateway Category = 502
	// CategoryServiceUnavailable 保留对应错误类别的稳定标识。
	CategoryServiceUnavailable Category = 503
	// CategoryGatewayTimeout 保留对应错误类别的稳定标识。
	CategoryGatewayTimeout Category = 504
)
