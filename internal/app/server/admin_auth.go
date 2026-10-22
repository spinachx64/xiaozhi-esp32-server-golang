package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// adminToken guards the /admin/* routes such as /admin/inject_msg.
func adminToken() string {
	if t := strings.TrimSpace(viper.GetString("admin_api.token")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("XIAOZHI_ADMIN_TOKEN"))
}

// checkAdminToken writes 401/503 and returns false unless the request carries
// the configured token. Without a token the routes are disabled.
func checkAdminToken(w http.ResponseWriter, r *http.Request) bool {
	want := adminToken()
	if want == "" {
		http.Error(w, "admin api disabled: set admin_api.token", http.StatusServiceUnavailable)
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
