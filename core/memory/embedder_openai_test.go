package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIEmbedder_Embed_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing auth header: %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req embeddingsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if req.Model == "" {
			t.Fatal("missing model")
		}
		out := embeddingsResponse{}
		for i, in := range req.Input {
			out.Data = append(out.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: []float32{float32(i + 1), 0, 0},
				Index:     i,
			})
			_ = in
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	emb := NewOpenAIEmbedder(
		func(_ context.Context) ([]byte, error) { return []byte("sk-test"), nil },
		WithOpenAIEndpoint(srv.URL),
		WithOpenAIHTTPClient(srv.Client()),
	)
	vecs, err := emb.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("vec len = %d, want 2", len(vecs))
	}
	if vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("unexpected vectors: %+v", vecs)
	}
	if emb.Dimensions() != 3 {
		t.Fatalf("Dimensions = %d, want 3 after Embed", emb.Dimensions())
	}
}

func TestOpenAIEmbedder_Embed_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	t.Cleanup(srv.Close)
	emb := NewOpenAIEmbedder(
		func(_ context.Context) ([]byte, error) { return []byte("sk-bad"), nil },
		WithOpenAIEndpoint(srv.URL),
		WithOpenAIHTTPClient(srv.Client()),
	)
	if _, err := emb.Embed(context.Background(), []string{"hi"}); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestNoopEmbedder_AlwaysErrors(t *testing.T) {
	t.Parallel()
	if _, err := (NoopEmbedder{}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected ErrEmbedderUnavailable")
	}
}
