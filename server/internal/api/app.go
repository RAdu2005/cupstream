package api

// AppAuthorizeRequest is the body of POST /api/app/authorize: the signed-in
// user approves the desktop app's sign-in request, whose parameters the app
// put in the /app/authorize page's query string.
type AppAuthorizeRequest struct {
	RedirectURI         string `json:"redirectUri"` // the app's loopback address
	State               string `json:"state"`
	CodeChallenge       string `json:"codeChallenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod" tstype:"'S256'"`
	DeviceName          string `json:"deviceName"`
}

// AppAuthorizeResponse tells the page where to send the browser: the app's
// redirect URI with a one-time code and the state.
type AppAuthorizeResponse struct {
	Redirect string `json:"redirect"`
}

// AppTokenRequest is the body of POST /api/app/token, sent by the desktop
// app to exchange its code.
type AppTokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"codeVerifier"`
	RedirectURI  string `json:"redirectUri"`
}

// AppToken is the desktop app's session: it sends the token as a Bearer
// token on /api/ requests, and the user can revoke it in their settings.
type AppToken struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// AppInstance is one of the user's desktop apps that is running and
// connected. ID is its session's ID.
type AppInstance struct {
	ID         string `json:"id"`
	DeviceName string `json:"deviceName"`
}

// AppsEvent lists the user's running desktop apps. Each of the user's
// connections gets it when it connects and whenever the list changes, so
// the website can offer going live from the app.
type AppsEvent struct {
	Type string        `json:"type" tstype:"'apps'"`
	Apps []AppInstance `json:"apps"`
}

// AppGoLive asks the desktop app to open its screen picker and go live,
// after the user chose the app on the website (client message appGoLive).
// Room is the room to stream into; empty means the user's start mode.
type AppGoLive struct {
	Type string `json:"type" tstype:"'goLive'"`
	Room string `json:"room,omitempty"`
}
