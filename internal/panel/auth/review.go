package auth

import (
	"crypto/subtle"
	"errors"
	"strings"
)

// ReviewSignIn is a sign-in for the App Store's reviewer, who can't read
// an inbox: its account's codes are a fixed six digits. It's off unless
// the Panel sets it (PANEL_REVIEW_EMAIL and PANEL_REVIEW_CODE), and is
// meant to be set only while the app is in review, for an account that
// belongs to nothing but the demo team. Wrong codes count as any others
// do (20 a day at most), so guessing the code would take decades.
type ReviewSignIn struct {
	Email string
	Code  string
}

// NewReviewSignIn checks a review sign-in's settings: both or neither.
func NewReviewSignIn(email, code string) (ReviewSignIn, error) {
	if email == "" && code == "" {
		return ReviewSignIn{}, nil
	}
	e, err := normalizeEmail(email)
	if err != nil || email == "" {
		return ReviewSignIn{}, errors.New("PANEL_REVIEW_EMAIL must be an email address")
	}
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return ReviewSignIn{}, errors.New("PANEL_REVIEW_CODE must be six digits")
	}
	return ReviewSignIn{Email: e, Code: code}, nil
}

// matches is whether email (normalized) is the reviewer's.
func (r ReviewSignIn) matches(email string) bool {
	return r.Email != "" && r.Code != "" && subtle.ConstantTimeCompare([]byte(r.Email), []byte(email)) == 1
}
