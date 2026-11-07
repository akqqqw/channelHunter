package apierror_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
)

func TestNewAssignsStatusByCode(t *testing.T) {
	cases := []struct {
		code   apierror.Code
		status int
	}{
		{apierror.CodeBadRequest, http.StatusBadRequest},
		{apierror.CodeValidation, http.StatusUnprocessableEntity},
		{apierror.CodeUnauthorized, http.StatusUnauthorized},
		{apierror.CodeForbidden, http.StatusForbidden},
		{apierror.CodeNotFound, http.StatusNotFound},
		{apierror.CodeConflict, http.StatusConflict},
		{apierror.CodeRateLimited, http.StatusTooManyRequests},
		{apierror.CodePlanLimit, http.StatusPaymentRequired},
		{apierror.CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{apierror.CodeInternal, http.StatusInternalServerError},
		{apierror.CodeUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			err := apierror.New(tc.code, "boom")
			assert.Equal(t, tc.status, err.Status)
			assert.Equal(t, tc.code, err.Code)
			assert.Equal(t, "boom", err.Message)
		})
	}
}

func TestUnknownCodeBecomesInternalServerError(t *testing.T) {
	err := apierror.New(apierror.Code("SOMETHING_NEW"), "boom")
	assert.Equal(t, http.StatusInternalServerError, err.Status)
}

func TestValidationCarriesFieldDetails(t *testing.T) {
	err := apierror.Validation(
		apierror.Field("name", "обязательное поле"),
		apierror.Field("budget", "должно быть больше 0"),
	)
	require.Len(t, err.Details, 2)
	assert.Equal(t, "name", err.Details[0].Field)
	assert.Equal(t, http.StatusUnprocessableEntity, err.Status)
}

func TestWrapKeepsCauseForLogging(t *testing.T) {
	cause := errors.New("connection reset by peer")
	err := apierror.Wrap(apierror.CodeUpstream, "провайдер недоступен", cause)

	assert.ErrorIs(t, err, cause)
	// The cause is present in the message for logs but must not be serialised
	// into the API payload (see http tests for the wire format).
	assert.Contains(t, err.Error(), "connection reset by peer")
	assert.Equal(t, cause, err.Cause())
}

func TestWithCauseDoesNotMutateOriginal(t *testing.T) {
	base := apierror.New(apierror.CodeInternal, "ошибка")
	wrapped := base.WithCause(errors.New("x"))

	assert.Nil(t, base.Cause())
	assert.NotNil(t, wrapped.Cause())
	assert.Equal(t, base.Message, wrapped.Message)
}

func TestFromDefaultsToInternal(t *testing.T) {
	raw := errors.New("database exploded")
	got := apierror.From(raw)

	assert.Equal(t, apierror.CodeInternal, got.Code)
	assert.Equal(t, http.StatusInternalServerError, got.Status)
	assert.Equal(t, raw, got.Cause())
}

func TestFromKeepsKnownErrors(t *testing.T) {
	original := apierror.New(apierror.CodeForbidden, "нет доступа")
	got := apierror.From(original)
	assert.Same(t, original, got)
}

func TestFromWrappedError(t *testing.T) {
	original := apierror.NotFound("канал")
	wrapped := fmt.Errorf("service layer: %w", original)

	got, ok := apierror.As(wrapped)
	require.True(t, ok)
	assert.Equal(t, apierror.CodeNotFound, got.Code)
	assert.Equal(t, http.StatusNotFound, apierror.StatusOf(wrapped))
}

func TestStatusOfUnknownError(t *testing.T) {
	assert.Equal(t, http.StatusInternalServerError, apierror.StatusOf(errors.New("boom")))
}

func TestHelpers(t *testing.T) {
	assert.Contains(t, apierror.NotFound("Канал").Message, "Канал")
	assert.Equal(t, apierror.CodeForbidden, apierror.Forbidden("").Code)
	assert.Equal(t, "Недостаточно прав для выполнения операции", apierror.Forbidden("").Message)
	assert.Equal(t, apierror.CodeUnauthorized, apierror.Unauthorized("").Code)
	assert.Equal(t, apierror.CodeInternal, apierror.Internal(errors.New("x")).Code)
}
