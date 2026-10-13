package config

import "testing"

const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// env returns a getenv for vars, with a valid secret key unless vars sets one.
func env(vars map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		if k == "CUPSTREAM_SECRET_KEY" {
			return testKey
		}
		return ""
	}
}

func TestDefaults(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.DevLogin || c.SecureCookies() || c.MediaMTXAPI.String() != "http://127.0.0.1:9997" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestErrors(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"dev login on public host": {"CUPSTREAM_DEV_LOGIN": "true", "CUPSTREAM_PUBLIC_URL": "https://cup.example.com"},
		"bad bool":                 {"CUPSTREAM_DEV_LOGIN": "yes please"},
		"bad url":                  {"CUPSTREAM_MEDIAMTX_API": "127.0.0.1:9997"},
		"half tls":                 {"CUPSTREAM_TLS_CERT": "cert.pem"},
		"bad log level":            {"CUPSTREAM_LOG_LEVEL": "loud"},
		"missing secret key":       {"CUPSTREAM_SECRET_KEY": ""},
		"short secret key":         {"CUPSTREAM_SECRET_KEY": "c2hvcnQ="},
		"half google client":       {"CUPSTREAM_GOOGLE_CLIENT_ID": "id"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(env(vars)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDevLoginOnLocalHost(t *testing.T) {
	c, err := load(env(map[string]string{"CUPSTREAM_DEV_LOGIN": "1", "CUPSTREAM_PUBLIC_URL": "http://localhost:5173"}))
	if err != nil || !c.DevLogin {
		t.Fatalf("dev login should be allowed on localhost: %v", err)
	}
}

func TestIsLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "app.localhost": true, "cupstream.test": true,
		"127.0.0.1": true, "127.0.0.2": true, "::1": true, "[::1]": true,
		"cup.example.com": false, "10.0.0.1": false, "localhost.example.com": false, "test": false,
	} {
		if got := IsLocalHost(host); got != want {
			t.Errorf("IsLocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestDevLoginGeneratesSecretKey(t *testing.T) {
	c, err := load(env(map[string]string{"CUPSTREAM_DEV_LOGIN": "1", "CUPSTREAM_SECRET_KEY": ""}))
	if err != nil || len(c.SecretKey) != 32 || !c.SecretKeyGenerated {
		t.Fatalf("got %v, generated=%v", err, c != nil && c.SecretKeyGenerated)
	}
}
