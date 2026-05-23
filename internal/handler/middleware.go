package handler

import (
	"net/http"
	"time"
)

const cookieName = "copa_session"
const cookieDuration = 365 * 24 * time.Hour

// setCookie sets the persistent session cookie.
func setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(cookieDuration),
		MaxAge:   int(cookieDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// getCookieToken reads the session cookie value (empty if not set).
func getCookieToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// clearCookie removes the session cookie.
func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    cookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
}
