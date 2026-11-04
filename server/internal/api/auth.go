package api

// Me is the response of GET /api/me.
type Me struct {
	User User `json:"user"`
	// Admin is set for admins (CUPSTREAM_ADMINS), who see the admin page.
	Admin bool `json:"admin,omitempty"`
}

// AuthProviders is the response of GET /api/auth/providers: the sign-in
// methods this server offers.
type AuthProviders struct {
	Providers []string `json:"providers" tstype:"AuthProvider[]"`
}

// DevLoginRequest is the body of POST /api/auth/dev/login.
type DevLoginRequest struct {
	Handle string `json:"handle"`
}

// PendingSignUp is the response of GET /api/auth/signup: a new user who
// signed in with a provider, and chooses their handle before their account
// is created.
type PendingSignUp struct {
	Provider    string `json:"provider" tstype:"AuthProvider"`
	Username    string `json:"username"` // at the provider
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Handle      string `json:"handle"` // a free handle to suggest
}

// SignUpRequest is the body of POST /api/auth/signup, which creates the
// account, signs the new user in and answers with Me.
type SignUpRequest struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

// HandleCheck is the response of GET /api/auth/handle?handle=: whether the
// signed-in user, or a new one, may take the handle, or why not.
type HandleCheck struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}
