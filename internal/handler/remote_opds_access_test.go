package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nowen-reader/nowen-reader/internal/middleware"
)

func TestRemoteOPDSAccess(t *testing.T) {
	router := setupTestRouter(t)
	adminSession := registerAndLogin(t, router)
	_, userKey := createOPDSTestUserAndKey(t, "remote-opds-user", "remote-opds-user")
	for _, tc := range []struct {
		name, path, session, key string
		want                     int
	}{
		{"anonymous cannot browse", "/api/remote-opds", "", "", http.StatusUnauthorized},
		{"user reaches catalog validation", "/api/remote-opds", "", userKey, http.StatusBadRequest},
		{"user cannot import", "/api/remote-opds/import", "", userKey, http.StatusForbidden},
		{"admin reaches import validation", "/api/remote-opds/import", adminSession, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			if tc.session != "" {
				req.AddCookie(&http.Cookie{Name: middleware.SessionCookie, Value: tc.session})
			}
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}
		})
	}
}
