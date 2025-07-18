package gauss

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/temirov/GAuss/pkg/constants"
	"github.com/temirov/GAuss/pkg/session"
	"golang.org/x/oauth2"
)

// helper to create service and handlers for tests
func newTestHandlers(t *testing.T) *Handlers {
	session.NewSession([]byte("secret"))
	svc, err := NewService("id", "secret", "http://localhost:8080", "/dashboard", ScopeStrings(DefaultScopes), "")
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := NewHandlers(svc)
	if err != nil {
		t.Fatal(err)
	}
	return handlers
}

func TestLoginRedirect(t *testing.T) {
	h := newTestHandlers(t)
	req := httptest.NewRequest("GET", constants.GoogleAuthPath, nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc == "" {
		t.Fatal("missing redirect location")
	}
	if len(rr.Header()["Set-Cookie"]) == 0 {
		t.Fatal("expected session cookie")
	}
}

func TestCallbackSuccess(t *testing.T) {
	// Mock OAuth2 token and userinfo endpoints
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"abc","token_type":"bearer"}`)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"email":   "e@example.com",
			"name":    "tester",
			"picture": "pic",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	h := newTestHandlers(t)

	// override endpoints
	h.service.config.Endpoint = oauth2.Endpoint{
		AuthURL:   server.URL + "/auth",
		TokenURL:  server.URL + "/token",
		AuthStyle: oauth2.AuthStyleInParams,
	}

	orig := userInfoEndpoint
	userInfoEndpoint = server.URL + "/userinfo"
	defer func() { userInfoEndpoint = orig }()

	// prepare request with session containing state
	req := httptest.NewRequest("GET", constants.CallbackPath+"?state=s123&code=c1", nil)
	initRR := httptest.NewRecorder()
	sess, _ := session.Store().Get(req, constants.SessionName)
	sess.Values["oauth_state"] = "s123"
	sess.Save(req, initRR)
	cookie := initRR.Result().Cookies()[0]
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	h.Callback(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rr.Code)
	}
	loc, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("location error: %v", err)
	}
	if loc.Path != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %s", loc.Path)
	}
	// verify session now contains user
	resCookie := rr.Result().Cookies()[0]
	chkReq := httptest.NewRequest("GET", "/", nil)
	chkReq.AddCookie(resCookie)
	sess2, _ := session.Store().Get(chkReq, constants.SessionName)
	if sess2.Values[constants.SessionKeyUserEmail] != "e@example.com" {
		t.Fatalf("user not stored in session")
	}
	if sess2.Values[constants.SessionKeyOAuthToken] == nil {
		t.Fatalf("oauth token not stored")
	}
}
