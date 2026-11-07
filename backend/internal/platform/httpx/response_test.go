package httpx_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

func request(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	return r.WithContext(observability.WithRequestID(r.Context(), "req-123"))
}

func TestJSONSetsHeadersAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.OK(rec, request(t, http.MethodGet, "/", ""), map[string]string{"hello": "world"})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"hello":"world"}`, rec.Body.String())
}

func TestFailUsesUniformEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Fail(rec, request(t, http.MethodPost, "/api/v1/channels", ""),
		apierror.Validation(apierror.Field("username", "обязательное поле")))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
			Details   []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	assert.Equal(t, "req-123", body.Error.RequestID)
	require.Len(t, body.Error.Details, 1)
	assert.Equal(t, "username", body.Error.Details[0].Field)
}

func TestFailHidesInternalDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Fail(rec, request(t, http.MethodGet, "/", ""),
		apierror.Internal(errors.New("pq: password authentication failed for user")))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "password")
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	rec := httptest.NewRecorder()
	err := httpx.DecodeJSON(rec, request(t, http.MethodPost, "/", `{"name":"x","surprise":1}`), &dst)

	require.Error(t, err)
	apiErr, ok := apierror.As(err)
	require.True(t, ok)
	assert.Equal(t, apierror.CodeValidation, apiErr.Code)
}

func TestDecodeJSONRejectsWrongType(t *testing.T) {
	var dst struct {
		Limit int `json:"limit"`
	}
	rec := httptest.NewRecorder()
	err := httpx.DecodeJSON(rec, request(t, http.MethodPost, "/", `{"limit":"ten"}`), &dst)

	require.Error(t, err)
	apiErr, ok := apierror.As(err)
	require.True(t, ok)
	assert.Equal(t, apierror.CodeValidation, apiErr.Code)
	require.Len(t, apiErr.Details, 1)
	assert.Equal(t, "limit", apiErr.Details[0].Field)
}

func TestDecodeJSONRejectsEmptyBody(t *testing.T) {
	var dst map[string]any
	rec := httptest.NewRecorder()
	err := httpx.DecodeJSON(rec, request(t, http.MethodPost, "/", ""), &dst)

	require.Error(t, err)
	apiErr, _ := apierror.As(err)
	assert.Equal(t, apierror.CodeValidation, apiErr.Code)
}

func TestDecodeJSONRejectsWrongContentType(t *testing.T) {
	r := request(t, http.MethodPost, "/", `{"a":1}`)
	r.Header.Set("Content-Type", "text/plain")

	var dst map[string]any
	err := httpx.DecodeJSON(httptest.NewRecorder(), r, &dst)

	require.Error(t, err)
	apiErr, _ := apierror.As(err)
	assert.Equal(t, apierror.CodeUnsupported, apiErr.Code)
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", 2<<20) + `"}`

	var dst struct {
		Name string `json:"name"`
	}
	err := httpx.DecodeJSON(httptest.NewRecorder(), request(t, http.MethodPost, "/", big), &dst)

	require.Error(t, err)
	apiErr, _ := apierror.As(err)
	assert.Equal(t, apierror.CodePayloadTooLarge, apiErr.Code)
}

func TestDecodeJSONAcceptsValidPayload(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	r := request(t, http.MethodPost, "/", `{"name":"ChannelHunter"}`)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")

	require.NoError(t, httpx.DecodeJSON(httptest.NewRecorder(), r, &dst))
	assert.Equal(t, "ChannelHunter", dst.Name)
}

func TestQueryInt(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?limit=25", nil)
	v, err := httpx.QueryInt(r, "limit", 10, 1, 100)
	require.NoError(t, err)
	assert.Equal(t, 25, v)

	// default
	v, err = httpx.QueryInt(r, "offset", 7, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 7, v)

	// out of range
	_, err = httpx.QueryInt(httptest.NewRequest(http.MethodGet, "/?limit=500", nil), "limit", 10, 1, 100)
	require.Error(t, err)

	// not a number
	_, err = httpx.QueryInt(httptest.NewRequest(http.MethodGet, "/?limit=abc", nil), "limit", 10, 1, 100)
	require.Error(t, err)
}

func TestQueryBool(t *testing.T) {
	v, err := httpx.QueryBool(httptest.NewRequest(http.MethodGet, "/?archived=true", nil), "archived", false)
	require.NoError(t, err)
	assert.True(t, v)

	v, err = httpx.QueryBool(httptest.NewRequest(http.MethodGet, "/", nil), "archived", true)
	require.NoError(t, err)
	assert.True(t, v)

	_, err = httpx.QueryBool(httptest.NewRequest(http.MethodGet, "/?archived=maybe", nil), "archived", false)
	require.Error(t, err)
}
