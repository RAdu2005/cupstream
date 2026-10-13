package config

import "testing"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
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
