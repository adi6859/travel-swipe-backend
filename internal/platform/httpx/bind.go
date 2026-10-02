package httpx

import (
	stdErrors "errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

var setupOnce sync.Once

// SetupValidator makes validation errors report JSON field names. Call once at startup.
func SetupValidator() {
	setupOnce.Do(func() {
		v, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			return
		}
		v.RegisterTagNameFunc(func(f reflect.StructField) string {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				return ""
			}
			if name == "" {
				return f.Name
			}
			return name
		})
	})
}

// BindJSON decodes and validates the request body, returning an invalid_argument
// AppError with per-field details on failure.
func BindJSON(c *gin.Context, dst any) error {
	err := c.ShouldBindJSON(dst)
	if err == nil {
		return nil
	}

	var verrs validator.ValidationErrors
	if stdErrors.As(err, &verrs) {
		fields := make(map[string]string, len(verrs))
		for _, fe := range verrs {
			fields[fieldPath(fe)] = describe(fe)
		}
		return apperrors.InvalidFields("request validation failed", fields)
	}

	var maxBytes *http.MaxBytesError
	if stdErrors.As(err, &maxBytes) {
		return apperrors.Invalid("request body too large")
	}
	if stdErrors.Is(err, io.EOF) {
		return apperrors.Invalid("request body is required")
	}
	return apperrors.Invalid("malformed JSON body")
}

func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return fe.Field()
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return "must be at least " + fe.Param() + " characters"
	case "max":
		return "must be at most " + fe.Param() + " characters"
	case "len":
		return "must be exactly " + fe.Param() + " characters"
	case "numeric":
		return "must contain only digits"
	case "e164":
		return "must be an E.164 phone number, e.g. +919876543210"
	case "uuid", "uuid4", "uuid7":
		return "must be a valid UUID"
	case "oneof":
		return "must be one of: " + fe.Param()
	case "url", "http_url":
		return "must be a valid URL"
	default:
		return "is invalid"
	}
}
