package deploy_test

import (
	"os"
	"strings"
	"testing"
)

// Compose and deploy.sh wait on /readyz. Caddy stays on /healthz so a
// database outage still shows the app's retry panel (single app container).
func TestReadyzGatesComposeAndDeploy(t *testing.T) {
	compose, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), `"/app/server", "-healthcheck", "http://127.0.0.1:8080/readyz"`) {
		t.Fatal("compose.yaml app healthcheck must probe /readyz")
	}
	if strings.Contains(string(compose), "8080/healthz") {
		t.Fatal("compose.yaml app healthcheck must not probe /healthz")
	}

	script, err := os.ReadFile("deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "-healthcheck http://127.0.0.1:8080/readyz") {
		t.Fatal("deploy.sh must gate on /readyz")
	}

	for _, path := range []string{"Caddyfile", "Caddyfile.local"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "health_uri /healthz") {
			t.Fatalf("%s must keep Caddy on /healthz", path)
		}
		if strings.Contains(string(src), "health_uri /readyz") {
			t.Fatalf("%s must not route on /readyz", path)
		}
	}
}
