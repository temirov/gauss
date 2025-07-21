package main

import (
	"encoding/json"
	"flag"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/temirov/GAuss/pkg/constants"
	"github.com/temirov/GAuss/pkg/gauss"
	"github.com/temirov/GAuss/pkg/session"
	"github.com/temirov/utils/system"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

const (
	DashboardPath = "/youtube"
	Root          = "/"
	appBase       = "http://localhost:8080/"
)

var logger *zap.Logger

func initLogger() {
	config := zap.NewDevelopmentConfig()
	config.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	var err error
	logger, err = config.Build()
	if err != nil {
		panic(err)
	}
}

func main() {
	initLogger()
	defer logger.Sync()

	loginTemplateFlag := flag.String("template", "", "Path to custom login template (empty for default)")
	flag.Parse()

	clientSecret := system.GetEnvOrFail("SESSION_SECRET")
	googleClientID := system.GetEnvOrFail("GOOGLE_CLIENT_ID")
	googleClientSecret := system.GetEnvOrFail("GOOGLE_CLIENT_SECRET")

	session.NewSession([]byte(clientSecret))

	scopes := gauss.ScopeStrings([]gauss.Scope{gauss.ScopeProfile, gauss.ScopeEmail, gauss.ScopeYouTubeReadonly})
	authService, err := gauss.NewService(googleClientID, googleClientSecret, appBase, DashboardPath, scopes, *loginTemplateFlag)
	if err != nil {
		logger.Fatal("Failed to initialize auth service", zap.Error(err))
	}

	authHandlers, err := gauss.NewHandlers(authService)
	if err != nil {
		logger.Fatal("Failed to initialize handlers", zap.Error(err))
	}

	mux := http.NewServeMux()
	authHandlers.RegisterRoutes(mux)

	templates, err := template.ParseGlob("examples/youtube_listing/templates/*.html")
	if err != nil {
		logger.Fatal("Failed to parse templates", zap.Error(err))
	}

	mux.Handle(DashboardPath, requestLogger(gauss.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderYouTube(w, r, authService, templates)
	}))))

	mux.Handle(Root, gauss.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, DashboardPath, http.StatusFound)
	})))

	logger.Info("Server starting", zap.String("port", "8080"))
	logger.Fatal("Server failed", zap.Error(http.ListenAndServe("localhost:8080", mux)))
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		logger.Info("Request started",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("user_agent", r.UserAgent()),
			zap.String("remote_addr", r.RemoteAddr),
		)

		next.ServeHTTP(w, r)

		logger.Info("Request completed",
			zap.String("path", r.URL.Path),
			zap.Duration("duration", time.Since(start)),
		)
	})
}

func renderYouTube(w http.ResponseWriter, r *http.Request, svc *gauss.Service, t *template.Template) {
	logger.Info("YouTube render started", zap.String("user_agent", r.UserAgent()))

	sess, err := session.Store().Get(r, constants.SessionName)
	if err != nil {
		logger.Error("Session get failed", zap.Error(err))
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	tokJSON, ok := sess.Values[constants.SessionKeyOAuthToken].(string)
	if !ok {
		logger.Error("OAuth token missing from session", zap.String("session_id", sess.ID))
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}

	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokJSON), &token); err != nil {
		logger.Error("Token unmarshal failed", zap.Error(err))
		http.Error(w, "Invalid authentication token", http.StatusInternalServerError)
		return
	}

	logger.Debug("Token details",
		zap.Bool("has_access_token", token.AccessToken != ""),
		zap.Bool("has_refresh_token", token.RefreshToken != ""),
		zap.Bool("is_expired", token.Expiry.Before(time.Now())),
	)

	if token.AccessToken == "" {
		logger.Error("Empty access token")
		http.Error(w, "Invalid access token", http.StatusUnauthorized)
		return
	}

	httpClient := svc.GetClient(r.Context(), &token)
	ytService, err := youtube.NewService(r.Context(), option.WithHTTPClient(httpClient))
	if err != nil {
		logger.Error("YouTube service creation failed", zap.Error(err))
		http.Error(w, "YouTube service unavailable", http.StatusInternalServerError)
		return
	}

	channels, err := ytService.Channels.List([]string{"contentDetails"}).Mine(true).Do()
	if err != nil {
		logger.Error("YouTube channels fetch failed",
			zap.Error(err),
			zap.Bool("is_oauth_error", isOAuthError(err)),
		)

		if isOAuthError(err) {
			logger.Info("OAuth error detected, redirecting to logout")
			http.Redirect(w, r, "/logout", http.StatusFound)
			return
		}

		http.Error(w, "Failed to access YouTube data", http.StatusInternalServerError)
		return
	}

	if len(channels.Items) == 0 {
		logger.Info("No YouTube channels found")
		http.Error(w, "No YouTube channel found", http.StatusNotFound)
		return
	}

	uploads := channels.Items[0].ContentDetails.RelatedPlaylists.Uploads
	vids, err := ytService.PlaylistItems.List([]string{"snippet"}).PlaylistId(uploads).MaxResults(10).Do()
	if err != nil {
		logger.Error("Playlist items fetch failed", zap.Error(err))
		http.Error(w, "Failed to load videos", http.StatusInternalServerError)
		return
	}

	logger.Info("YouTube videos loaded successfully", zap.Int("count", len(vids.Items)))

	if err := t.ExecuteTemplate(w, "youtube_videos.html", vids.Items); err != nil {
		logger.Error("Template execution failed", zap.Error(err))
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

func isOAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "invalid_grant") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "access_denied") ||
		strings.Contains(errStr, "invalid_token")
}
