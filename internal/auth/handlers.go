package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Límite de intentos de login por cuenta: 10 seguidos y luego uno por minuto, vengan de la IP
// que vengan. Complementa el límite por IP, que no frena un ataque repartido entre muchas IPs.
//
// Cuenta por email, exista o no, así la respuesta no revela qué emails están registrados.
// El costo: quien conozca un email puede demorar ese login. La salida del dueño es recuperar
// la contraseña, que reinicia el límite (ver resetPassword).
const (
	loginLimitEvery = time.Minute
	loginLimitBurst = 10
)

// loginLimitKey es la clave de un email en el límite de intentos del login.
func loginLimitKey(email string) string {
	return "login:" + strings.ToLower(strings.TrimSpace(email))
}

// register responde POST /auth/register: crea un usuario con rol user e inicia su sesión.
// Con REQUIRE_EMAIL_VERIFICATION activa, también le envía el enlace para confirmar el email.
func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	var in RegisterInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	u, err := s.CreateUser(r.Context(), in, RoleUser)
	if err != nil {
		return err
	}

	if err := s.StartSession(r.Context(), w, u.ID); err != nil {
		return err
	}

	if s.opts.RequireVerifiedEmail {
		if err := s.sendVerificationEmail(r.Context(), u); err != nil {
			return err
		}
	}

	httpx.Write(w, http.StatusCreated, u)
	return nil
}

// login responde POST /auth/login. Si el usuario tiene el 2FA activo, todavía no crea la sesión:
// responde 202 {"two_factor_required": true} con la cookie del desafío, y el login se completa
// con POST /auth/two-factor/challenge.
func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var in loginInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	if !s.loginLimiter.Allow(loginLimitKey(in.Email)) {
		httpx.LoggerFrom(r.Context()).Warn("límite de intentos de login de la cuenta superado")
		w.Header().Set("Retry-After", strconv.Itoa(int(loginLimitEvery.Seconds())))
		return httpx.TooManyRequests()
	}

	u, err := s.Authenticate(r.Context(), in.Email, in.Password)
	if err != nil {
		return err
	}

	if u.TwoFactorEnabled {
		return s.startTwoFactorChallenge(w, r, u.ID)
	}

	if err := s.StartSession(r.Context(), w, u.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, u)
	return nil
}

// logout responde POST /auth/logout. Borra la sesión si existe y siempre le pide al navegador
// que borre la cookie, así que responde 204 aunque la sesión ya haya vencido.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	if err := s.EndSession(r.Context(), w, r); err != nil {
		return err
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// me responde GET /auth/me con el usuario de la sesión.
func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	httpx.Write(w, http.StatusOK, MustUser(r.Context()))
	return nil
}
