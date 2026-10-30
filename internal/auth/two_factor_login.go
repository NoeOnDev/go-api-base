package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// startTwoFactorChallenge is the first step of the login with two-factor authentication: the
// password was already correct, so a challenge that expires in 5 minutes is created and sent in
// an HttpOnly cookie.
func (s *Service) startTwoFactorChallenge(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := s.issueToken(r.Context(), userID, purposeTwoFactorLogin, twoFactorLoginTTL, nil)
	if err != nil {
		return err
	}
	s.setChallengeCookie(w, token, time.Now().Add(twoFactorLoginTTL))
	httpx.Write(w, http.StatusAccepted, map[string]bool{"two_factor_required": true})
	return nil
}

var errNoChallenge = httpx.Unauthorized("two_factor_challenge_expired")

var (
	errWrongRecoveryCode = httpx.Validation(httpx.Fields{"recovery_code": i18n.M("validation.invalid_recovery_code")})
	errTwoFactorLocked   = &httpx.Error{Status: http.StatusUnauthorized, Code: "two_factor_locked"}
)

// ChallengeCookieName is the cookie of the two-factor challenge: the login with the correct
// password sends it, and POST /auth/two-factor/challenge uses it to know which user is signing
// in. With CookieSecure it also has the __Host- prefix.
const ChallengeCookieName = "two_factor_challenge"

// Brakes against brute force on the code, done by someone who already has the password:
//   - Each challenge allows maxChallengeAttempts attempts; after that the password has to be entered again.
//   - Each user has twoFactorLimitBurst attempts in a row and then one every twoFactorLimitEvery
//     (10 per hour), from however many IPs. With 3 valid codes in a million, that is about 240
//     tries a day: close to a 0.07% chance of guessing.
//
// The per-user limit is in the database (internal/ratelimit): it holds for every instance.
const (
	maxChallengeAttempts = 5
	twoFactorLimitEvery  = 6 * time.Minute
	twoFactorLimitBurst  = 10
)

// twoFactorLimitKey is the user's key in the two-factor attempt limit.
func twoFactorLimitKey(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// challengeInput is a second factor: the app code or a recovery code, only one of them.
type challengeInput struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

func (in *challengeInput) normalize() {
	in.Code = strings.TrimSpace(in.Code)
	in.RecoveryCode = strings.TrimSpace(in.RecoveryCode)
}

func (in challengeInput) validate() error {
	if (in.Code == "") == (in.RecoveryCode == "") {
		return httpx.Validation(httpx.Fields{"code": i18n.M("validation.code_required")})
	}
	return nil
}

// twoFactorChallenge answers POST /auth/two-factor/challenge with the app code ({"code"}) or a
// recovery code ({"recovery_code"}). If it is correct, it creates the session. A wrong code can
// be retried up to maxChallengeAttempts times; once they are used up the challenge is deleted
// and the user is notified by email.
func (s *Service) twoFactorChallenge(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(s.cookieName(ChallengeCookieName))
	if err != nil {
		return errNoChallenge
	}
	tokenHash := hashToken(c.Value)

	// The body first: a malformed request does not spend attempts.
	var in challengeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	challenge := store.GetUserTokenUserParams{TokenHash: tokenHash, Purpose: purposeTwoFactorLogin, MaxAttempts: maxChallengeAttempts}
	userID, err := s.q.GetUserTokenUser(r.Context(), challenge)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoChallenge
	}
	if err != nil {
		return fmt.Errorf("find the two-factor challenge: %w", err)
	}

	allowed, err := s.twoFactorLimiter.Allow(r.Context(), twoFactorLimitKey(userID))
	if err != nil {
		return err
	}
	if !allowed {
		httpx.LoggerFrom(r.Context()).Warn("the user's two-factor attempt limit exceeded", "user_id", userID)
		w.Header().Set("Retry-After", s.twoFactorLimiter.RetryAfter())
		return httpx.TooManyRequests()
	}

	attempt, err := s.q.UseUserTokenAttempt(r.Context(), store.UseUserTokenAttemptParams(challenge))
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoChallenge // it expired or ran out between the two queries
	}
	if err != nil {
		return fmt.Errorf("record the two-factor attempt: %w", err)
	}

	stored, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", userID, err)
	}

	if err := s.checkSecondFactor(r, stored, in); err != nil {
		if attempt >= maxChallengeAttempts && (errors.Is(err, errWrongCode) || errors.Is(err, errWrongRecoveryCode)) {
			return s.lockChallenge(w, r, stored, tokenHash)
		}
		return err
	}

	if err := s.q.DeleteUserToken(r.Context(), tokenHash); err != nil {
		return fmt.Errorf("delete the two-factor challenge: %w", err)
	}
	if err := s.StartSession(w, r, userID); err != nil {
		return err
	}
	s.clearChallengeCookie(w)

	httpx.Write(w, http.StatusOK, toUser(stored))
	return nil
}

// checkSecondFactor checks the app code or the recovery code.
// A wrong code returns errWrongCode or errWrongRecoveryCode.
func (s *Service) checkSecondFactor(r *http.Request, u store.User, in challengeInput) error {
	if in.Code != "" {
		return s.useTOTPCode(r, u, in.Code)
	}
	ok, err := s.useRecoveryCode(r.Context(), u.ID, in.RecoveryCode)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongRecoveryCode
	}
	return nil
}

// lockChallenge ends the challenge after too many wrong codes: the password has to be entered
// again. Whoever got this far knows it, so the user is notified.
func (s *Service) lockChallenge(w http.ResponseWriter, r *http.Request, u store.User, tokenHash []byte) error {
	if err := s.q.DeleteUserToken(r.Context(), tokenHash); err != nil {
		return fmt.Errorf("delete the two-factor challenge: %w", err)
	}
	s.clearChallengeCookie(w)

	ip := httpx.ClientIP(r, s.opts.TrustedProxies)
	httpx.LoggerFrom(r.Context()).Warn("two-factor challenge locked after wrong codes", "user_id", u.ID, "ip", ip)
	s.sendTwoFactorLockedEmail(r.Context(), u, ip)

	return errTwoFactorLocked
}

// sendTwoFactorLockedEmail tells the user that someone with their password failed the
// two-factor code. If it cannot be sent, it is only logged: the answer to the client does not
// change.
func (s *Service) sendTwoFactorLockedEmail(ctx context.Context, u store.User, ip string) {
	log := httpx.LoggerFrom(ctx)
	allowed, err := s.allowEmail(ctx, "two_factor_locked", u.ID)
	if err != nil {
		log.Error("could not send the two-factor locked notice", "user_id", u.ID, "error", err)
		return
	}
	if !allowed {
		log.Warn("two-factor locked notice not sent: the user's email limit", "user_id", u.ID)
		return
	}
	err = s.sendEmail(ctx, u.Email, u.Locale, "two_factor_locked.txt", emailData{
		Name: u.Name,
		Link: s.opts.FrontendURL,
		// A format without ambiguity in any language (02/01 is January 2 or February 1).
		When: time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		IP:   ip,
	})
	if err != nil {
		log.Error("could not send the two-factor locked notice", "user_id", u.ID, "error", err)
	}
}

// useTOTPCode validates the app code and records its step. A code already used (for example,
// intercepted from a previous login) is rejected even if it is still within its time window.
func (s *Service) useTOTPCode(r *http.Request, u store.User, code string) error {
	if u.TOTPEnabledAt == nil {
		return errWrongCode
	}
	step, ok, err := s.checkTOTP(r.Context(), u, code)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongCode
	}

	rows, err := s.q.UseTOTPStep(r.Context(), store.UseTOTPStepParams{ID: u.ID, TOTPLastStep: &step})
	if err != nil {
		return fmt.Errorf("record the two-factor code: %w", err)
	}
	if rows == 0 {
		return errWrongCode // the code was already used
	}
	return nil
}

// setChallengeCookie sends the two-factor challenge cookie, with the same protection as the
// session one.
func (s *Service) setChallengeCookie(w http.ResponseWriter, token string, expires time.Time) {
	c := s.cookie(token, expires)
	c.Name = s.cookieName(ChallengeCookieName)
	http.SetCookie(w, c)
}

// clearChallengeCookie asks the browser to delete the challenge cookie.
func (s *Service) clearChallengeCookie(w http.ResponseWriter) {
	c := s.cookie("", time.Unix(0, 0))
	c.Name = s.cookieName(ChallengeCookieName)
	c.MaxAge = -1
	http.SetCookie(w, c)
}
