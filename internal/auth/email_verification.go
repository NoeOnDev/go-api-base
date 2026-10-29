package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// sendVerificationEmail sends the link to confirm the email. It invalidates the previous links.
func (s *Service) sendVerificationEmail(ctx context.Context, u User) error {
	token, err := s.issueToken(ctx, u.ID, purposeEmailVerification, emailVerificationTTL, nil)
	if err != nil {
		return err
	}
	return s.sendEmail(ctx, u.Email, u.Locale, "verify_email.txt", emailData{
		Name:    u.Name,
		Link:    s.frontendLink("/verify-email", token),
		Expires: s.expiresIn(u.Locale, emailVerificationTTL),
	})
}

type verifyEmailInput struct {
	Token string `json:"token"`
}

// verifyEmail answers POST /auth/email/verify with the token of the link. It is public: the
// token is enough to identify the user, so it works even if the link is opened on another device.
func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var in verifyEmailInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	userID, err := consumeToken(r.Context(), s.q, in.Token, purposeEmailVerification)
	if err != nil {
		return err
	}
	if err := s.q.MarkEmailVerified(r.Context(), userID); err != nil {
		return fmt.Errorf("mark the email verified: %w", err)
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// resendVerification answers POST /auth/email/resend: it sends another link to the user of the
// session. If the email is already verified it sends nothing and answers 204. After several
// sends (see allowEmail) it answers 429: the one asking is the user, so there is nothing to hide.
func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) error {
	u := MustUser(r.Context())
	if u.EmailVerified() {
		httpx.Write(w, http.StatusNoContent, nil)
		return nil
	}
	allowed, err := s.allowEmail(r.Context(), purposeEmailVerification, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		w.Header().Set("Retry-After", s.emailLimiter.RetryAfter())
		return errTooManyEmails
	}

	if err := s.sendVerificationEmail(r.Context(), u); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, map[string]string{
		"message": i18n.FromContext(r.Context()).T("messages.verification_sent"),
	})
	return nil
}
