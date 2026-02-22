package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// kvV2Response builds a Vault KV v2 JSON response body.
func kvV2Response(data map[string]any) []byte {
	resp := map[string]any{
		"data": map[string]any{
			"data": data,
			"metadata": map[string]any{
				"version": 1,
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func newTestVaultServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// clearVaultEnv prevents host environment from interfering with tests.
func clearVaultEnv(t *testing.T) {
	t.Helper()
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_NAMESPACE", "")
}

func TestVaultProvider_ResolveWithField(t *testing.T) {
	clearVaultEnv(t)

	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/data/myapp/db" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Vault-Token") != "test-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write(kvV2Response(map[string]any{
			"password": "s3cret",
			"username": "admin",
		}))
	})

	vp, err := NewVaultProvider(map[string]string{
		"address": srv.URL,
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	secret, err := vp.Resolve(context.Background(), "vault://secret/data/myapp/db#password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if secret.Value != "s3cret" {
		t.Errorf("got Value=%q, want %q", secret.Value, "s3cret")
	}
	if secret.Metadata["source"] != "vault" {
		t.Errorf("got source=%q, want %q", secret.Metadata["source"], "vault")
	}
	if secret.Metadata["field"] != "password" {
		t.Errorf("got field=%q, want %q", secret.Metadata["field"], "password")
	}
}

func TestVaultProvider_ResolveWithoutField(t *testing.T) {
	clearVaultEnv(t)

	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(kvV2Response(map[string]any{
			"password": "s3cret",
			"username": "admin",
		}))
	})

	vp, err := NewVaultProvider(map[string]string{
		"address": srv.URL,
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	secret, err := vp.Resolve(context.Background(), "vault://secret/data/myapp/db")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Should return JSON of entire data map.
	var data map[string]any
	if err := json.Unmarshal([]byte(secret.Value), &data); err != nil {
		t.Fatalf("Value is not valid JSON: %v", err)
	}
	if data["password"] != "s3cret" {
		t.Errorf("got password=%v, want %q", data["password"], "s3cret")
	}
	if data["username"] != "admin" {
		t.Errorf("got username=%v, want %q", data["username"], "admin")
	}
}

func TestVaultProvider_NonVaultRef(t *testing.T) {
	clearVaultEnv(t)

	vp, err := NewVaultProvider(map[string]string{
		"address": "http://localhost:8200",
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "env://MY_KEY")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestVaultProvider_NotFound(t *testing.T) {
	clearVaultEnv(t)

	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	vp, err := NewVaultProvider(map[string]string{
		"address": srv.URL,
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://secret/data/missing")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestVaultProvider_Forbidden(t *testing.T) {
	clearVaultEnv(t)

	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	vp, err := NewVaultProvider(map[string]string{
		"address": srv.URL,
		"token":   "bad-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://secret/data/app")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if errors.Is(err, ErrSecretNotFound) {
		t.Error("should NOT be ErrSecretNotFound for auth failure")
	}
}

func TestVaultProvider_MissingField(t *testing.T) {
	clearVaultEnv(t)

	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(kvV2Response(map[string]any{
			"username": "admin",
		}))
	})

	vp, err := NewVaultProvider(map[string]string{
		"address": srv.URL,
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://secret/data/app#nonexistent")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound for missing field, got %v", err)
	}
}

func TestVaultProvider_EnvOverride(t *testing.T) {
	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "env-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write(kvV2Response(map[string]any{"key": "value"}))
	})

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "env-token")
	t.Setenv("VAULT_NAMESPACE", "")

	vp, err := NewVaultProvider(map[string]string{
		"address": "http://should-be-overridden:8200",
		"token":   "should-be-overridden",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	secret, err := vp.Resolve(context.Background(), "vault://secret/data/test#key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if secret.Value != "value" {
		t.Errorf("got Value=%q, want %q", secret.Value, "value")
	}
}

func TestVaultProvider_NamespaceHeader(t *testing.T) {
	var gotNamespace string
	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotNamespace = r.Header.Get("X-Vault-Namespace")
		w.Write(kvV2Response(map[string]any{"k": "v"}))
	})

	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_NAMESPACE", "")

	vp, err := NewVaultProvider(map[string]string{
		"address":   srv.URL,
		"token":     "test-token",
		"namespace": "admin/team-a",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://secret/data/test#k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotNamespace != "admin/team-a" {
		t.Errorf("got namespace header=%q, want %q", gotNamespace, "admin/team-a")
	}
}

func TestVaultProvider_NamespaceEnvOverride(t *testing.T) {
	var gotNamespace string
	srv := newTestVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotNamespace = r.Header.Get("X-Vault-Namespace")
		w.Write(kvV2Response(map[string]any{"k": "v"}))
	})

	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_NAMESPACE", "env-namespace")

	vp, err := NewVaultProvider(map[string]string{
		"address":   srv.URL,
		"token":     "test-token",
		"namespace": "config-namespace",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://secret/data/test#k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotNamespace != "env-namespace" {
		t.Errorf("got namespace header=%q, want %q", gotNamespace, "env-namespace")
	}
}

func TestNewVaultProvider_MissingAddress(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	_, err := NewVaultProvider(map[string]string{"token": "t"})
	if err == nil {
		t.Fatal("expected error for missing address")
	}
}

func TestNewVaultProvider_MissingToken(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	_, err := NewVaultProvider(map[string]string{"address": "http://localhost:8200"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestVaultProvider_EmptyPath(t *testing.T) {
	clearVaultEnv(t)

	vp, err := NewVaultProvider(map[string]string{
		"address": "http://localhost:8200",
		"token":   "test-token",
	})
	if err != nil {
		t.Fatalf("NewVaultProvider: %v", err)
	}

	_, err = vp.Resolve(context.Background(), "vault://")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound for empty path, got %v", err)
	}
}
