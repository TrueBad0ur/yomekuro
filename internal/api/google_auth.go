package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truebad0ur/yomekuro/internal/auth"
)

const (
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleUserInfoURL  = "https://openidconnect.googleapis.com/v1/userinfo"
	googleStateCookie  = "google_oauth_state"
)

var googleHTTPClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) authProviders(w http.ResponseWriter, _ *http.Request) {
	respond(w, map[string]bool{"google": s.googleOAuth.Enabled()})
}

func (s *Server) googleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.googleOAuth.Enabled() {
		http.NotFound(w, r)
		return
	}

	state, err := randomOAuthState()
	if err != nil {
		respondInternal(w, "could not start Google login", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     googleStateCookie,
		Value:    state,
		Path:     "/api/auth/google",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   10 * 60,
	})

	params := url.Values{
		"client_id":     {s.googleOAuth.ClientID},
		"redirect_uri":  {s.googleOAuth.RedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"prompt":        {"select_account"},
	}
	http.Redirect(w, r, googleAuthorizeURL+"?"+params.Encode(), http.StatusFound)
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.googleOAuth.Enabled() {
		http.NotFound(w, r)
		return
	}
	clearGoogleStateCookie(w, r)

	if providerError := r.URL.Query().Get("error"); providerError != "" {
		redirectGoogleError(w, r, "Google sign-in was cancelled")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(googleStateCookie)
	if err != nil || !secureEqual(state, cookie.Value) {
		redirectGoogleError(w, r, "Google sign-in expired; please try again")
		return
	}
	if code == "" {
		redirectGoogleError(w, r, "Google did not return an authorization code")
		return
	}

	accessToken, err := s.exchangeGoogleCode(r, code)
	if err != nil {
		slog.Warn("Google OAuth code exchange failed", "err", err)
		redirectGoogleError(w, r, "Google sign-in failed")
		return
	}
	identity, err := fetchGoogleIdentity(r, accessToken)
	if err != nil {
		slog.Warn("Google OAuth userinfo failed", "err", err)
		redirectGoogleError(w, r, "Google account could not be verified")
		return
	}
	if !identity.EmailVerified {
		redirectGoogleError(w, r, "Google account email is not verified")
		return
	}

	user, err := auth.GetOrCreateGoogleUser(r.Context(), s.db, identity.Subject, identity.Email)
	if err != nil {
		slog.Error("Google user lookup/create failed", "err", err)
		redirectGoogleError(w, r, "Could not create the local account")
		return
	}
	token, err := auth.CreateSession(r.Context(), s.db, user.ID)
	if err != nil {
		slog.Error("Google session creation failed", "err", err)
		redirectGoogleError(w, r, "Could not create a session")
		return
	}
	setSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) exchangeGoogleCode(r *http.Request, code string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {s.googleOAuth.ClientID},
		"client_secret": {s.googleOAuth.ClientSecret},
		"redirect_uri":  {s.googleOAuth.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := googleHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token response did not contain access_token")
	}
	return payload.AccessToken, nil
}

type googleIdentity struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

func fetchGoogleIdentity(r *http.Request, accessToken string) (googleIdentity, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return googleIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := googleHTTPClient.Do(req)
	if err != nil {
		return googleIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return googleIdentity{}, fmt.Errorf("userinfo endpoint returned %s", resp.Status)
	}
	var identity googleIdentity
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&identity); err != nil {
		return googleIdentity{}, err
	}
	if identity.Subject == "" || identity.Email == "" {
		return googleIdentity{}, fmt.Errorf("userinfo response missing subject or email")
	}
	return identity, nil
}

func randomOAuthState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func clearGoogleStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     googleStateCookie,
		Value:    "",
		Path:     "/api/auth/google",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func redirectGoogleError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(message), http.StatusSeeOther)
}
