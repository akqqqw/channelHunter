// Package httpx contains small HTTP helpers shared by every handler: JSON
// encoding, the uniform error envelope, body size limits and pagination.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
)

// maxJSONBody bounds request bodies decoded by handlers.
const maxJSONBody = 1 << 20 // 1 MiB

// ErrorEnvelope is the wire format of every API error.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the error payload.
type ErrorBody struct {
	Code      apierror.Code         `json:"code"`
	Message   string                `json:"message"`
	RequestID string                `json:"request_id"`
	Details   []apierror.FieldError `json:"details,omitempty"`
}

// Meta carries pagination information.
type Meta struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ListResponse is the standard collection envelope.
type ListResponse[T any] struct {
	Data []T  `json:"data"`
	Meta Meta `json:"meta"`
}

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent; only logging remains.
		observability.LoggerFrom(r.Context()).Error("encode response",
			slog.String("error", err.Error()))
	}
}

// OK writes 200 with v.
func OK(w http.ResponseWriter, r *http.Request, v any) { JSON(w, r, http.StatusOK, v) }

// Created writes 201 with v.
func Created(w http.ResponseWriter, r *http.Request, v any) { JSON(w, r, http.StatusCreated, v) }

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Fail writes the uniform error envelope. Unknown errors become 500 and are
// logged with their cause; they never leak internal details to the client.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := apierror.From(err)
	requestID := observability.RequestIDFrom(r.Context())
	log := observability.LoggerFrom(r.Context())

	if apiErr.Status >= http.StatusInternalServerError {
		log.Error("request failed",
			slog.String("error", apiErr.Error()),
			slog.String("code", string(apiErr.Code)),
			slog.String("path", r.URL.Path),
			slog.String("method", r.Method),
		)
	} else {
		log.Debug("request rejected",
			slog.String("error", apiErr.Error()),
			slog.String("code", string(apiErr.Code)),
		)
	}

	JSON(w, r, apiErr.Status, ErrorEnvelope{Error: ErrorBody{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		RequestID: requestID,
		Details:   apiErr.Details,
	}})
}

// DecodeJSON strictly decodes a JSON request body into dst.
//
// Unknown fields are rejected (a typo must not be silently ignored), bodies are
// size-limited and a missing body is reported as a validation error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mediaType := strings.TrimSpace(strings.Split(ct, ";")[0]); mediaType != "application/json" {
			return apierror.New(apierror.CodeUnsupported, "Ожидается Content-Type: application/json")
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError

		switch {
		case errors.As(err, &maxErr):
			return apierror.New(apierror.CodePayloadTooLarge, "Тело запроса слишком велико")
		case errors.Is(err, io.EOF):
			return apierror.New(apierror.CodeValidation, "Тело запроса обязательно")
		case errors.As(err, &syntaxErr):
			return apierror.Newf(apierror.CodeValidation, "Некорректный JSON (позиция %d)", syntaxErr.Offset)
		case errors.As(err, &typeErr):
			return apierror.Validation(apierror.Field(typeErr.Field,
				fmt.Sprintf("Ожидается значение типа %s", typeErr.Type.String())))
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return apierror.Validation(apierror.Field(strings.Trim(field, `"`), "Неизвестное поле"))
		default:
			return apierror.Wrap(apierror.CodeValidation, "Не удалось разобрать тело запроса", err)
		}
	}

	// reject trailing data such as {"a":1}{"b":2}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apierror.New(apierror.CodeValidation, "В теле запроса допускается только один JSON-объект")
	}
	return nil
}

// QueryInt reads an optional integer query parameter.
func QueryInt(r *http.Request, key string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apierror.Validation(apierror.Field(key, "Ожидается целое число"))
	}
	if v < min || v > max {
		return 0, apierror.Validation(apierror.Field(key,
			fmt.Sprintf("Допустимый диапазон: %d..%d", min, max)))
	}
	return v, nil
}

// QueryBool reads an optional boolean query parameter.
func QueryBool(r *http.Request, key string, def bool) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apierror.Validation(apierror.Field(key, "Ожидается true или false"))
	}
	return v, nil
}
