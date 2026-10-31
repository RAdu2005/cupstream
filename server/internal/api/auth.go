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
