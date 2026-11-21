package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

func newEvent(method string, headers map[string]string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, "/", nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()

	return &core.RequestEvent{
		Event: router.Event{Response: recorder, Request: request},
	}, recorder
}

func TestJSONMatchesExpress(t *testing.T) {
	e, recorder := newEvent(http.MethodGet, nil)

	if err := JSON(e, 200, struct {
		Status string `json:"status"`
	}{Status: "UP"}); err != nil {
		t.Fatal(err)
	}

	if recorder.Code != 200 {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"UP"}` {
		t.Errorf("body = %q", got)
	}
	// Verified against the Node server with `curl -i`.
	if got, want := recorder.Header().Get("ETag"), `W/"f-RQ8OySFd+KR+AvtJ7qImjtT0D/0"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
}

func TestTextIsHTML(t *testing.T) {
	e, recorder := newEvent(http.MethodGet, nil)

	if err := Text(e, 200, "development"); err != nil {
		t.Fatal(err)
	}

	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got, want := recorder.Header().Get("ETag"), `W/"b-BRNEJrzRdQULCBk8AvFMza/MoIg"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
	if got := recorder.Body.String(); got != "development" {
		t.Errorf("body = %q", got)
	}
}

func TestError(t *testing.T) {
	e, recorder := newEvent(http.MethodGet, nil)

	if err := Error(e); err != nil {
		t.Fatal(err)
	}

	if recorder.Code != 500 {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"error","reason":"internal-error"}` {
		t.Errorf("body = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestJSONSetsCharset(t *testing.T) {
	e, recorder := newEvent(http.MethodGet, nil)

	if err := JSON(e, 422, map[string]string{"status": "error"}); err != nil {
		t.Fatal(err)
	}

	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestConditionalRequestIsFresh(t *testing.T) {
	tag := ETag([]byte(`{"status":"UP"}`))

	e, recorder := newEvent(http.MethodGet, map[string]string{"If-None-Match": tag})
	if err := JSON(e, 200, map[string]string{"status": "UP"}); err != nil {
		t.Fatal(err)
	}

	if recorder.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want it removed on 304", got)
	}
}

func TestMarshalDoesNotEscapeHTML(t *testing.T) {
	body, err := Marshal(map[string]string{"value": "<a>&b</a>"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), `{"value":"<a>&b</a>"}`; got != want {
		t.Errorf("Marshal = %q, want %q", got, want)
	}
}
