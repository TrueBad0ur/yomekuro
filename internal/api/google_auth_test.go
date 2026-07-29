package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthProviders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  GoogleOAuthConfig
		enabled string
	}{
		{name: "disabled", enabled: `"google":false`},
		{
			name: "enabled",
			config: GoogleOAuthConfig{
				ClientID:     "client",
				ClientSecret: "secret",
				RedirectURL:  "https://reader.example/api/auth/google/callback",
			},
			enabled: `"google":true`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{googleOAuth: tc.config}
			rec := httptest.NewRecorder()
			s.authProviders(rec, httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if body := rec.Body.String(); !contains(body, tc.enabled) {
				t.Fatalf("body = %q, want %q", body, tc.enabled)
			}
		})
	}
}

func TestGoogleLoginRedirect(t *testing.T) {
	s := &Server{googleOAuth: GoogleOAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://reader.example/api/auth/google/callback",
	}}
	req := httptest.NewRequest(http.MethodGet, "https://reader.example/api/auth/google", nil)
	rec := httptest.NewRecorder()
	s.googleLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if query.Get("client_id") != "client-id" {
		t.Errorf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != s.googleOAuth.RedirectURL {
		t.Errorf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("scope") != "openid email profile" {
		t.Errorf("scope = %q", query.Get("scope"))
	}
	state := query.Get("state")
	if len(state) < 32 {
		t.Fatalf("state is too short: %q", state)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != googleStateCookie {
		t.Fatalf("cookies = %#v", cookies)
	}
	if cookies[0].Value != state || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Errorf("state cookie = %#v", cookies[0])
	}
}

func TestGoogleLoginDisabled(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.googleLogin(rec, httptest.NewRequest(http.MethodGet, "/api/auth/google", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
