package gauss

// Scope represents a Google OAuth2 scope string.
type Scope string

const (
	// ScopeEmail allows retrieving the user's email address.
	ScopeEmail Scope = "email"
	// ScopeProfile allows retrieving basic profile information.
	ScopeProfile Scope = "profile"
	// ScopeYouTubeReadonly allows read-only access to YouTube resources.
	ScopeYouTubeReadonly Scope = "https://www.googleapis.com/auth/youtube.readonly"
)

// DefaultScopes lists the scopes used when none are provided to NewService.
var DefaultScopes = []Scope{ScopeProfile, ScopeEmail}

// ScopeStrings converts a slice of Scope values into their string representations.
func ScopeStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}
