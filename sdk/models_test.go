package docsgpt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"auth_error"}}`)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"a-1","object":"model","name":"Support","owned_by":"docsgpt"}]}`)
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL+"/", "good").Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "a-1" || models[0].Name != "Support" {
		t.Fatalf("Models() = %+v, %v", models, err)
	}
	_, err = NewClient(srv.URL, "bad").Models(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Models() with a bad key: err = %v", err)
	}
}
